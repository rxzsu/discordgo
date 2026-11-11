package discordgo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// loopbackWS starts a minimal gateway-like websocket server and returns a
// client connection plus a cleanup func. The server drains everything the
// client writes so the write path can be measured end to end.
func loopbackWS(b *testing.B) (*websocket.Conn, func()) {
	b.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		defer c.Close(websocket.StatusInternalError, "bench done")
		for {
			if _, _, err := c.Read(ctx); err != nil {
				return
			}
		}
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+srv.Listener.Addr().String(), nil)
	if err != nil {
		srv.Close()
		b.Fatal(err)
	}
	return conn, func() {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		srv.Close()
	}
}

// BenchmarkGatewayWriteHeartbeat measures the heartbeat ws write as shipped:
// wsjson.Write marshalling heartbeatOp through encoding/json.
func BenchmarkGatewayWriteHeartbeat(b *testing.B) {
	conn, cleanup := loopbackWS(b)
	defer cleanup()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := wsjson.Write(context.Background(), conn, heartbeatOp{1, int64(i)}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGatewayWriteHeartbeatRaw measures the same frame written as a
// hand-encoded text message: {"op":1,"d":<seq>} has a fixed shape, so a
// stack buffer plus conn.Write replaces the reflection-based Marshal.
func BenchmarkGatewayWriteHeartbeatRaw(b *testing.B) {
	conn, cleanup := loopbackWS(b)
	defer cleanup()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var buf [40]byte
		const prefix = `{"op":1,"d":`
		copy(buf[:], prefix)
		p := strconv.AppendInt(buf[:len(prefix)], int64(i), 10)
		n := len(p)
		buf[n] = '}'
		if err := conn.Write(context.Background(), websocket.MessageText, buf[:n+1]); err != nil {
			b.Fatal(err)
		}
	}
}
