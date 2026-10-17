package proxy

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"
)

// The SSE observer sits in the response path: whatever it parses, the bytes
// it forwards must be exactly the bytes it read, however reads are chunked.
func FuzzSSEObserver(f *testing.F) {
	f.Add([]byte("event: message\r\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\r\n\r\n"), uint8(7))
	f.Add([]byte("id: 1\ndata: a\ndata: b\n\n:comment\n\ndata:\n\n"), uint8(1))
	f.Add([]byte("data: no newline at end"), uint8(255))
	f.Fuzz(func(t *testing.T, stream []byte, chunk uint8) {
		var events [][]byte
		o := &sseObserver{
			ReadCloser: io.NopCloser(&chunkReader{b: stream, n: int(chunk)%64 + 1}),
			frame:      func(b []byte) { events = append(events, append([]byte(nil), b...)) },
		}
		got, err := io.ReadAll(o)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, stream) {
			t.Fatalf("observer altered the stream:\n in %q\nout %q", stream, got)
		}
		for _, e := range events {
			if len(e) == 0 {
				t.Fatal("dispatched an empty event")
			}
		}
	})
}

// chunkReader returns at most n bytes per Read, to vary how events straddle reads.
type chunkReader struct {
	b []byte
	n int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.b) == 0 {
		return 0, io.EOF
	}
	n := min(r.n, len(p), len(r.b))
	copy(p, r.b[:n])
	r.b = r.b[n:]
	return n, nil
}

// Decompression of untrusted request bodies must stay within the limit.
func FuzzGunzip(f *testing.F) {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	zw.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	zw.Close()
	f.Add(b.Bytes())
	f.Add([]byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 0xff})
	f.Fuzz(func(t *testing.T, in []byte) {
		out, err := gunzip(in)
		if err == nil && len(out) > maxObserved {
			t.Fatalf("gunzip returned %d bytes, over the %d limit", len(out), maxObserved)
		}
	})
}
