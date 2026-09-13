package bot

import (
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func queueMsg(id, channel string) *discordgo.Message {
	return &discordgo.Message{ID: id, ChannelID: channel}
}

func waitForID(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("generated %q, want %q", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for generation of %q", want)
	}
}

// markBusy puts a channel into the state it is in while a generation runs, so a
// test can drive drain synchronously and feed it a burst deterministically.
func markBusy(q *queue, channelID string) {
	state := q.stateFor(channelID)
	state.mu.Lock()
	state.busy = true
	state.mu.Unlock()
}

func TestQueueProcessesFirstMessage(t *testing.T) {
	got := make(chan string, 4)
	q := newQueue(func(m *discordgo.Message) { got <- m.ID })

	// Regression: submit used to spawn the drain loop without the message, so
	// the first (and only) message was silently dropped.
	q.submit(queueMsg("1", "c"))
	waitForID(t, got, "1")
}

func TestQueueCollapsesBurstToNewest(t *testing.T) {
	var (
		mu   sync.Mutex
		seen []string
	)
	q := newQueue(func(m *discordgo.Message) {
		mu.Lock()
		seen = append(seen, m.ID)
		mu.Unlock()
	})

	markBusy(q, "c")
	q.submit(queueMsg("2", "c"))
	q.submit(queueMsg("3", "c"))
	q.drain("c", queueMsg("1", "c"))

	mu.Lock()
	defer mu.Unlock()
	want := []string{"1", "3"}
	if len(seen) != len(want) {
		t.Fatalf("generated %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("generated %v, want %v", seen, want)
		}
	}
}

func TestQueueSerializesGenerations(t *testing.T) {
	var (
		mu          sync.Mutex
		inFlight    int
		maxInFlight int
		order       []string
	)
	q := newQueue(func(m *discordgo.Message) {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		order = append(order, m.ID)
		mu.Unlock()

		time.Sleep(5 * time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()
	})

	markBusy(q, "c")
	q.submit(queueMsg("2", "c"))
	q.drain("c", queueMsg("1", "c"))

	mu.Lock()
	defer mu.Unlock()
	if maxInFlight != 1 {
		t.Fatalf("max concurrent generations = %d, want 1", maxInFlight)
	}
	if len(order) != 2 || order[0] != "1" || order[1] != "2" {
		t.Fatalf("generated %v, want [1 2]", order)
	}
}

func TestQueueSurvivesPanicAndStaysUsable(t *testing.T) {
	var (
		mu     sync.Mutex
		seen   []string
		panics []string
	)
	q := newQueue(func(m *discordgo.Message) {
		mu.Lock()
		seen = append(seen, m.ID)
		mu.Unlock()
		if m.ID == "1" {
			panic("boom")
		}
	})
	q.onPanic = func(m *discordgo.Message, _ any) {
		mu.Lock()
		panics = append(panics, m.ID)
		mu.Unlock()
	}

	markBusy(q, "c")
	q.submit(queueMsg("2", "c"))
	q.drain("c", queueMsg("1", "c"))

	mu.Lock()
	if len(panics) != 1 || panics[0] != "1" {
		mu.Unlock()
		t.Fatalf("recovered panics = %v, want [1]", panics)
	}
	if len(seen) != 2 || seen[1] != "2" {
		mu.Unlock()
		t.Fatalf("generated %v, want [1 2]", seen)
	}
	mu.Unlock()

	state := q.stateFor("c")
	state.mu.Lock()
	busy := state.busy
	state.mu.Unlock()
	if busy {
		t.Fatal("channel is still busy after a panic; it would never reply again")
	}
}

func TestQueueRunsAgainAfterIdle(t *testing.T) {
	got := make(chan string, 4)
	q := newQueue(func(m *discordgo.Message) { got <- m.ID })

	q.submit(queueMsg("1", "c"))
	waitForID(t, got, "1")

	// Proves busy was cleared, otherwise this submit would only set pending.
	q.submit(queueMsg("2", "c"))
	waitForID(t, got, "2")
}
