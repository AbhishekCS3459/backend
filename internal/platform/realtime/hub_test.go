package realtime

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func pending(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestHubPublishesToTopicSubscribersOnly(t *testing.T) {
	hub := NewHub(0)
	a, stopA, err := hub.Subscribe("store-a")
	require.NoError(t, err)
	b, stopB, err := hub.Subscribe("store-b")
	require.NoError(t, err)
	defer stopB()

	hub.Publish("store-a")
	require.True(t, pending(a))
	require.False(t, pending(b))

	// Signals to a subscriber that hasn't read yet merge into one.
	hub.Publish("store-a")
	hub.Publish("store-a")
	require.True(t, pending(a))
	require.False(t, pending(a))

	hub.PublishAll()
	require.True(t, pending(a))
	require.True(t, pending(b))

	stopA()
	stopA()
	require.Equal(t, 1, hub.Subscribers())
	hub.Publish("store-a")
	require.False(t, pending(a))
}

func TestHubLimitsAndClose(t *testing.T) {
	hub := NewHub(1)
	_, stop, err := hub.Subscribe("store")
	require.NoError(t, err)
	_, _, err = hub.Subscribe("store")
	require.ErrorIs(t, err, ErrTooManySubscribers)

	stop()
	_, stop, err = hub.Subscribe("store")
	require.NoError(t, err)
	defer stop()

	hub.Close()
	hub.Close()
	select {
	case <-hub.Done():
	default:
		t.Fatal("Done should be closed")
	}
	_, _, err = hub.Subscribe("other")
	require.Error(t, err)
}
