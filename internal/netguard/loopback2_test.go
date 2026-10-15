package netguard

import (
	"context"
	"net"
	"net/http"
)

// listenLoopback2 listens on 127.0.0.2, a second loopback address.
func listenLoopback2() (net.Listener, error) {
	var lc net.ListenConfig
	return lc.Listen(context.Background(), "tcp", "127.0.0.2:0")
}

// fetchErr issues a GET and closes the body; it returns only the error.
func fetchErr(c *http.Client, url string) error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
