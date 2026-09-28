package control

import (
	"context"
	"sync"

	"github.com/alex-bluetrain/notuya-go/pkg/bulb"
	"github.com/alex-bluetrain/notuya-go/pkg/device"
)

// Registry owns one Control per configured device, keyed by device_id. It is
// the daemon's device layer: handlers look a device up by id and drive it, and
// rooms/scenes fan out across the members they name.
type Registry struct {
	mu         sync.RWMutex
	order      []string // device_ids in config order
	controls   map[string]*Control
	streamOpts bulb.StreamOptions
}

// NewRegistry builds a Registry from a device list. Controls are created eagerly
// but sessions open lazily on first use.
func NewRegistry(devices []Device, streamOpts bulb.StreamOptions) *Registry {
	r := &Registry{
		controls:   make(map[string]*Control, len(devices)),
		streamOpts: streamOpts,
	}
	for _, d := range devices {
		r.order = append(r.order, d.DeviceID)
		r.controls[d.DeviceID] = New(d, streamOpts)
	}
	return r
}

// Get returns the Control for a device_id, or nil if unknown.
func (r *Registry) Get(deviceID string) *Control {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.controls[deviceID]
}

// Devices returns every configured device in config order.
func (r *Registry) Devices() []Device {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Device, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.controls[id].Device())
	}
	return out
}

// ControlsFor returns the Controls matching the given device_ids, skipping any
// that are unknown (stale references are tolerated).
func (r *Registry) ControlsFor(deviceIDs []string) []*Control {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Control, 0, len(deviceIDs))
	for _, id := range deviceIDs {
		if c := r.controls[id]; c != nil {
			out = append(out, c)
		}
	}
	return out
}

// Replace swaps the device set (used after a config edit). Controls no longer
// present are closed; controls that remain keep their live sessions.
func (r *Registry) Replace(devices []Device) {
	r.mu.Lock()
	defer r.mu.Unlock()

	next := make(map[string]*Control, len(devices))
	var order []string
	for _, d := range devices {
		order = append(order, d.DeviceID)
		if existing := r.controls[d.DeviceID]; existing != nil && existing.Device() == d {
			next[d.DeviceID] = existing
		} else {
			if existing != nil {
				existing.Close()
			}
			next[d.DeviceID] = New(d, r.streamOpts)
		}
	}
	for id, c := range r.controls {
		if _, kept := next[id]; !kept {
			c.Close()
		}
	}
	r.controls = next
	r.order = order
}

// CloseAll tears down every session.
func (r *Registry) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.controls {
		c.Close()
	}
}

// forEach runs fn against the given controls concurrently and reports whether
// all succeeded. Per-control errors are isolated (best-effort fan-out).
func forEach(controls []*Control, fn func(*Control) error) bool {
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := true
	for _, c := range controls {
		wg.Add(1)
		go func(c *Control) {
			defer wg.Done()
			if err := fn(c); err != nil {
				mu.Lock()
				ok = false
				mu.Unlock()
			}
		}(c)
	}
	wg.Wait()
	return ok
}

// SetPowerMany turns the given controls on or off concurrently, best-effort.
func SetPowerMany(ctx context.Context, controls []*Control, on bool) bool {
	return forEach(controls, func(c *Control) error {
		return c.SetPower(ctx, on)
	})
}

// ApplyScene fans a scene out to the lights it names, best-effort, using the
// registry to resolve device_ids. Unknown ids are skipped.
func (r *Registry) ApplyScene(ctx context.Context, scene Scene) bool {
	controls := make([]*Control, 0, len(scene.States))
	states := make([]SceneState, 0, len(scene.States))
	for _, st := range scene.States {
		if c := r.Get(st.DeviceID); c != nil {
			controls = append(controls, c)
			states = append(states, st)
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := true
	for i := range controls {
		wg.Add(1)
		go func(c *Control, st SceneState) {
			defer wg.Done()
			if err := c.ApplyState(ctx, st); err != nil {
				mu.Lock()
				ok = false
				mu.Unlock()
			}
		}(controls[i], states[i])
	}
	wg.Wait()
	return ok
}

// LiveDrag is a live colour drag across a set of controls. It borrows each
// control's music-mode streamer for the drag and returns them to command mode
// when closed.
type LiveDrag struct {
	controls []*Control
}

// BeginLiveDrag starts a live drag across the given controls, seeded with the
// current colour.
func BeginLiveDrag(controls []*Control, seed device.RGB) *LiveDrag {
	for _, c := range controls {
		c.BeginLiveDrag(seed)
	}
	return &LiveDrag{controls: controls}
}

// Update streams a colour to every control (newest-wins, non-blocking).
func (d *LiveDrag) Update(rgb device.RGB, transition *int) {
	for _, c := range d.controls {
		c.UpdateLiveDrag(rgb, transition)
	}
}

// Close ends the drag, leaving each bulb on its last colour.
func (d *LiveDrag) Close() {
	for _, c := range d.controls {
		c.EndLiveDrag()
	}
}

// CaptureScene builds a scene named name from the current live status of every
// configured device. Devices that fail to refresh are recorded as off.
func (r *Registry) CaptureScene(ctx context.Context, name string) Scene {
	devices := r.Devices()
	states := make([]SceneState, len(devices))
	var wg sync.WaitGroup
	for i, d := range devices {
		wg.Add(1)
		go func(i int, d Device) {
			defer wg.Done()
			c := r.Get(d.DeviceID)
			st, err := c.Refresh(ctx)
			if err != nil {
				states[i] = SceneState{DeviceID: d.DeviceID, On: false}
				return
			}
			states[i] = StateFromStatus(d.DeviceID, st)
		}(i, d)
	}
	wg.Wait()
	return Scene{Name: name, States: states}
}
