package bot

import (
	"sync"

	"github.com/bwmarrin/discordgo"
)

// channelState serialises work per channel and remembers the newest message
// that arrived while a generation was already running.
type channelState struct {
	genMu   sync.Mutex // held for the duration of one generation
	mu      sync.Mutex // guards busy and pending
	busy    bool
	pending *discordgo.Message
}

// queue runs one generation per channel at a time. Messages that arrive while a
// generation is in flight collapse into a single follow-up that uses only the
// newest of them; the caller still stores every one, so the next turn sees
// them. The handler is injected so the coalescing logic can be tested without a
// Discord session or a model call.
type queue struct {
	run     func(*discordgo.Message)
	onPanic func(*discordgo.Message, any)

	mu     sync.Mutex
	states map[string]*channelState
}

// newQueue builds an idle queue that calls run for each message it decides to
// answer. onPanic is optional and reports a recovered panic from run.
func newQueue(run func(*discordgo.Message)) *queue {
	return &queue{
		run:    run,
		states: make(map[string]*channelState),
	}
}

func (q *queue) stateFor(channelID string) *channelState {
	q.mu.Lock()
	defer q.mu.Unlock()
	state, ok := q.states[channelID]
	if !ok {
		state = &channelState{}
		q.states[channelID] = state
	}
	return state
}

// submit queues a message for generation, starting a drain loop if the channel
// is idle. It returns immediately; generation runs on its own goroutine.
func (q *queue) submit(msg *discordgo.Message) {
	state := q.stateFor(msg.ChannelID)
	state.mu.Lock()
	if state.busy {
		state.pending = msg
		state.mu.Unlock()
		return
	}
	state.busy = true
	state.mu.Unlock()

	go q.drain(msg.ChannelID, msg)
}

// drain answers first and then, in a loop, the newest message that arrived
// while the previous one was being answered. It runs until the channel has no
// pending work. The busy flag is cleared in the same critical section that
// observes the empty pending slot, so a submission racing the exit either
// starts a fresh drain or is picked up here, never lost.
func (q *queue) drain(channelID string, first *discordgo.Message) {
	state := q.stateFor(channelID)
	clean := false
	defer func() {
		if clean {
			return
		}
		// Only reached if something panicked outside generate's recover.
		state.mu.Lock()
		state.busy = false
		state.mu.Unlock()
	}()

	msg := first
	for {
		if msg != nil {
			q.generate(state, msg)
		}
		state.mu.Lock()
		msg = state.pending
		state.pending = nil
		if msg == nil {
			state.busy = false
			clean = true
			state.mu.Unlock()
			return
		}
		state.mu.Unlock()
	}
}

// generate runs one handler call under the channel's generation lock so it
// never interleaves with a slash command, and contains any panic to this turn.
func (q *queue) generate(state *channelState, msg *discordgo.Message) {
	state.genMu.Lock()
	defer state.genMu.Unlock()
	defer func() {
		if r := recover(); r != nil && q.onPanic != nil {
			q.onPanic(msg, r)
		}
	}()
	q.run(msg)
}

// exclusive runs fn while holding the channel's generation lock, so a slash
// command never interleaves with a reply already being written.
func (q *queue) exclusive(channelID string, fn func()) {
	state := q.stateFor(channelID)
	state.genMu.Lock()
	defer state.genMu.Unlock()
	fn()
}
