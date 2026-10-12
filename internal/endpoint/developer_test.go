package endpoint

import (
	"context"
	"errors"
	"net"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// boundListener stands in for a listener on one port.
type boundListener struct {
	net.Listener
	port int
}

// scriptedListen binds every port that busy does not name and records the
// order of the attempts.
func scriptedListen(busy func(port int) bool, attempts *[]int) Listener {
	return func(_ context.Context, _ string, port int) (net.Listener, error) {
		*attempts = append(*attempts, port)
		if busy(port) {
			return nil, &BusyError{Port: port}
		}
		return boundListener{port: port}, nil
	}
}

func sequence(values ...int) func(int) int {
	next := 0
	return func(n int) int {
		value := values[next%len(values)] % n
		next++
		return value
	}
}

// TestDevFallsBackInOrder pins the developer bind order: the standard port,
// 7373, 7678, then the random range, and the first free port wins.
func TestDevFallsBackInOrder(t *testing.T) {
	ports := DeveloperPorts(7827, sequence(4, 999, 0))
	require.Equal(t, []int{7827, 7373, 7678, 7004, 7999, 7000}, ports[:6])
	require.Len(t, ports, 24)
	require.Equal(t, 0, ports[len(ports)-1], "the last port binds any free port")

	for _, test := range []struct {
		name  string
		busy  []int
		bound int
	}{
		{name: "standard port free", bound: 7827},
		{name: "standard port busy", busy: []int{7827}, bound: 7373},
		{name: "both standard alternates busy", busy: []int{7827, 7373}, bound: 7678},
		{name: "fixed ports busy", busy: []int{7827, 7373, 7678}, bound: 7004},
	} {
		t.Run(test.name, func(t *testing.T) {
			var attempts []int
			listener, err := BindFirst(t.Context(), "127.0.0.1", ports,
				scriptedListen(func(port int) bool { return slices.Contains(test.busy, port) }, &attempts))
			require.NoError(t, err)
			require.Equal(t, test.bound, listener.(boundListener).port)
			require.Equal(t, append(slices.Clone(test.busy), test.bound), attempts, "bind tries ports in order and stops at the first free port")
		})
	}
}

// TestDevFallsBackToAnyPortAfterTwentyRandomFailures binds port 0 only after
// the fixed ports and twenty random ports are busy.
func TestDevFallsBackToAnyPortAfterTwentyRandomFailures(t *testing.T) {
	ports := DeveloperPorts(7827, sequence(11, 22, 33, 44, 55))
	var attempts []int
	listener, err := BindFirst(t.Context(), "127.0.0.1", ports,
		scriptedListen(func(port int) bool { return port != 0 }, &attempts))
	require.NoError(t, err)
	require.Equal(t, 0, listener.(boundListener).port)
	require.Len(t, attempts, 24)
	require.Equal(t, []int{7827, 7373, 7678}, attempts[:3])
	random := attempts[3:23]
	require.Len(t, random, 20)
	for _, port := range random {
		require.GreaterOrEqual(t, port, 7000)
		require.LessOrEqual(t, port, 7999)
	}
	require.Equal(t, 0, attempts[23])
}

func TestBindFirstStopsOnAnotherError(t *testing.T) {
	refused := errors.New("permission denied")
	var attempts []int
	_, err := BindFirst(t.Context(), "127.0.0.1", []int{7827, 7373}, func(_ context.Context, _ string, port int) (net.Listener, error) {
		attempts = append(attempts, port)
		return nil, refused
	})
	require.ErrorIs(t, err, refused)
	require.Equal(t, []int{7827}, attempts, "an error other than a busy port ends the search")
}

func TestBindFirstNamesTheFirstBusyPort(t *testing.T) {
	var attempts []int
	_, err := BindFirst(t.Context(), "127.0.0.1", []int{7827, 7373},
		scriptedListen(func(int) bool { return true }, &attempts))
	busy, ok := errors.AsType[*BusyError](err)
	require.True(t, ok)
	require.Equal(t, 7827, busy.Port)
	require.EqualError(t, err, "port 7827 is busy")
}
