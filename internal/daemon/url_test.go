package daemon

import "testing"

func TestSocketURL(t *testing.T) {
	cases := map[string]string{
		"https://dymmer.com":          "wss://dymmer.com/daemon/websocket",
		"https://dymmer.com/":         "wss://dymmer.com/daemon/websocket",
		"http://localhost:4000":       "ws://localhost:4000/daemon/websocket",
		" https://dymmer.com ":        "wss://dymmer.com/daemon/websocket",
		"https://example.org/dymmer/": "wss://example.org/dymmer/daemon/websocket",
		// nothing that could carry a credential or a state survives
		"https://user:pass@dymmer.com/?token=x#frag": "wss://dymmer.com/daemon/websocket",
	}
	for in, want := range cases {
		got, err := SocketURL(in)
		if err != nil || got != want {
			t.Errorf("SocketURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestSocketURLRefusesWhatIsNotAServer(t *testing.T) {
	for _, in := range []string{"", "dymmer.com", "ftp://dymmer.com", "wss://dymmer.com", "https://", "://x"} {
		if got, err := SocketURL(in); err == nil {
			t.Errorf("SocketURL(%q) = %q, want an error", in, got)
		}
	}
}
