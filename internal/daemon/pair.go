package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrPairingExpired is the end of a PIN that nobody entered in Dymmer in time.
var ErrPairingExpired = errors.New("the PIN expired before it was entered in Dymmer: run `dym auth daemon` again")

// PairOptions says how to pair this host.
type PairOptions struct {
	// URL is the websocket endpoint of Dymmer's daemon socket.
	URL string
	// StatePath is where the host and its permanent secret are kept once Dymmer gives them.
	StatePath string
	// Inventory is what to tell Dymmer about this host, so that the host is created already filled in.
	Inventory Inventory
	// OnPIN is called as soon as Dymmer hands out the PIN, to show it to whoever is at the console.
	OnPIN func(pin string, validFor time.Duration)
}

// Pair asks Dymmer for a PIN and waits, on the same connection, until somebody enters it in Dymmer. Dymmer then
// creates the host and sends its permanent secret down the open connection; that is saved before Pair returns, so
// the daemon can reconnect by itself from now on.
func Pair(ctx context.Context, o PairOptions) (State, error) {
	conn, err := Dial(ctx, o.URL, nil)
	if err != nil {
		return State{}, err
	}
	defer conn.Close()

	reply, err := conn.Join(ctx, "daemon:pair", map[string]any{"inventory": o.Inventory.Payload()})
	if err != nil {
		return State{}, err
	}

	pin, _ := reply["pin"].(string)
	if pin == "" {
		return State{}, errors.New("Dymmer did not hand out a PIN")
	}
	seconds, _ := reply["expires_in"].(float64)
	if o.OnPIN != nil {
		o.OnPIN(pin, time.Duration(seconds)*time.Second)
	}

	for {
		select {
		case event, open := <-conn.Events():
			if !open {
				return State{}, fmt.Errorf("the connection with Dymmer ended before the PIN was entered: %w", conn.Err())
			}
			switch event.Name {
			case "paired":
				return keepPairing(o.StatePath, event.Payload)
			case "expired":
				return State{}, ErrPairingExpired
			}
		case <-ctx.Done():
			return State{}, ctx.Err()
		}
	}
}

func keepPairing(path string, payload map[string]any) (State, error) {
	hostID, _ := payload["host_id"].(string)
	secret, _ := payload["secret"].(string)
	if hostID == "" || secret == "" {
		return State{}, errors.New("Dymmer sent an incomplete pairing")
	}

	state := State{HostID: hostID, Secret: secret}
	if err := SaveState(path, state); err != nil {
		return State{}, fmt.Errorf("the host %s was created in Dymmer but its secret could not be saved (%v): delete that host in Dymmer and pair again", hostID, err)
	}
	return state, nil
}
