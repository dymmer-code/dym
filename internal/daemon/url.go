package daemon

import (
	"fmt"
	"net/url"
	"strings"
)

// SocketURL turns the address of Dymmer (https://dymmer.com) into the websocket endpoint of its daemon socket.
// Plain http is accepted for a Dymmer that runs on this machine or inside a private network.
func SocketURL(server string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(server))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q is not the address of a Dymmer server (expected https://dymmer.com)", server)
	}

	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("the address of the server must start with https:// or http://, got %q", server)
	}

	u.Path = strings.TrimRight(u.Path, "/") + "/daemon/websocket"
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return u.String(), nil
}
