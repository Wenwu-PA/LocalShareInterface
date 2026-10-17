package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSSEHTTP2SurvivesIdleWriteDeadline(t *testing.T) {
	c := newClient(t)
	c.setup()
	srv := httptest.NewUnstartedServer(c.server.Handler(nil))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	client := srv.Client()
	client.Jar = c.http.Jar
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/events", nil)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.ProtoMajor != 2 || response.StatusCode != 200 {
		t.Fatalf("unexpected stream: %s %d", response.Proto, response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	readEvent := func() string {
		t.Helper()
		var data strings.Builder
		for {
			line, e := reader.ReadString('\n')
			if e != nil {
				t.Fatal(e)
			}
			data.WriteString(line)
			if line == "\n" {
				return data.String()
			}
		}
	}
	if !strings.Contains(readEvent(), `"snapshot"`) {
		t.Fatal("missing snapshot")
	}
	timer := time.AfterFunc(6*time.Second, func() { c.server.publish("idle_probe", map[string]bool{"ok": true}) })
	defer timer.Stop()
	if !strings.Contains(readEvent(), `"idle_probe"`) {
		t.Fatal("missing event after idle")
	}
}
