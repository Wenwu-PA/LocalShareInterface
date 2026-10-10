package notify

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSMTPRefusesUnencryptedServer(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		io.WriteString(conn, "220 localhost test\r\n")
		reader := bufio.NewReader(conn)
		reader.ReadString('\n')
		io.WriteString(conn, "250 localhost\r\n")
		reader.ReadString('\n')
	}()
	t.Setenv("LANBRIDGE_SMTP_URL", "smtp://"+listener.Addr().String())
	t.Setenv("LANBRIDGE_SMTP_FROM", "from@example.invalid")
	t.Setenv("LANBRIDGE_SMTP_TO", "to@example.invalid")
	c := Channel{Name: "SMTP", Kind: "smtp", Cooldown: 5, URLenv: "LANBRIDGE_SMTP_URL", FromEnv: "LANBRIDGE_SMTP_FROM", ToEnv: "LANBRIDGE_SMTP_TO"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	e = Deliver(ctx, c, Event{Kind: "test"})
	if e == nil || !strings.Contains(e.Error(), "STARTTLS") {
		t.Fatal(e)
	}
	<-done
}
