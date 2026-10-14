// Local integration fixture. It never contacts Cloudflare or creates a tunnel.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func main() {
	origin := ""
	for i, arg := range os.Args {
		if arg == "--url" && i+1 < len(os.Args) {
			origin = os.Args[i+1]
		}
	}
	if origin == "" {
		os.Exit(2)
	}
	executable, _ := os.Executable()
	data, _ := json.Marshal(map[string]any{"origin": origin, "pid": os.Getpid()})
	if err := os.WriteFile(executable+".json", data, 0600); err != nil {
		os.Exit(3)
	}
	fmt.Fprintln(os.Stderr, "https://lanbridge-test.trycloudflare.com")
	for {
		time.Sleep(time.Second)
	}
}
