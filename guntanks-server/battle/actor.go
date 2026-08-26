package battle

import (
	"context"
	"guntanks-server/engine"
	"time"
)

const introDuration = 1500 * time.Millisecond
const introSpawnY = -200.0

type Command struct {
	Type, TankID, Direction string
	Power                   float64
	Weapon                  engine.Weapon
	Reply                   chan Event
}
type Event struct {
	Type  string
	State engine.State
	Shot  *engine.Shot
	Error error
}
type Actor struct {
	State         engine.State
	Commands      chan Command
	Events        chan Event
	Tick          time.Duration
	ctx           context.Context
	cancel        context.CancelFunc
	terrain       *engine.TerrainMask
	timeout       time.Duration
	moveDirection string
	aimDirection  string
	lastBroadcast time.Time
	pausedAt      time.Time
	paused        bool
	introComplete bool
}

func NewActor(s engine.State, hz int) *Actor {
	if hz <= 0 {
		hz = 60
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Actor{State: s, Commands: make(chan Command, 64), Events: make(chan Event, 128), Tick: time.Second / time.Duration(hz), timeout: 30 * time.Second, ctx: ctx, cancel: cancel}
}
func (a *Actor) Configure(terrain *engine.TerrainMask, timeout time.Duration) {
	a.terrain = terrain
	if timeout > 0 {
		a.timeout = timeout
	}
	a.applyIntro(terrain)
}
func (a *Actor) applyIntro(terrain *engine.TerrainMask) {
	a.State.IntroEndMS = time.Now().Add(introDuration).UnixMilli()
	a.State.TurnDeadlineMS = a.State.IntroEndMS + a.timeout.Milliseconds()
	for i := range a.State.Tanks {
		tank := &a.State.Tanks[i]
		landY := 300.0
		if terrain != nil {
			for n := 0; n < terrain.Height; n++ {
				if terrain.SolidAtRect(int(tank.X), n+30, 25, 1) {
					landY = float64(n)
					break
				}
			}
		}
		tank.LandY = landY
		tank.Y = introSpawnY
	}
}
func (a *Actor) Start() {
	if a.State.TurnDeadlineMS == 0 {
		a.State.TurnDeadlineMS = time.Now().Add(a.timeout).UnixMilli()
	}
	go func() {
		ticker := time.NewTicker(a.Tick)
		defer ticker.Stop()
		defer close(a.Events)
		for {
			select {
			case <-a.ctx.Done():
				return
			case c := <-a.Commands:
				a.apply(c)
			case <-ticker.C:
				a.tick()
			}
		}
	}()
}
func (a *Actor) Stop() { a.cancel() }
func (a *Actor) tick() {
	if a.paused {
		return
	}
	if a.State.Phase == "finished" {
		return
	}
	if a.State.IntroEndMS > 0 && time.Now().UnixMilli() < a.State.IntroEndMS {
		return
	}
	if a.State.IntroEndMS > 0 && !a.introComplete {
		a.introComplete = true
		a.State.ApplyIntroComplete(a.terrain)
		if a.State.TurnDeadlineMS <= a.State.IntroEndMS {
			a.State.TurnDeadlineMS = time.Now().Add(a.timeout).UnixMilli()
		}
		a.Events <- Event{Type: "battle.intro_complete", State: a.State}
		if a.State.Phase == "finished" {
			a.moveDirection, a.aimDirection = "", ""
			return
		}
	}
	changed := false
	if a.moveDirection != "" {
		if tank, err := a.State.Tank(a.State.CurrentTankID); err == nil && tank.Moved < 80 {
			distance := 1.5
			if remaining := 80 - tank.Moved; remaining < distance {
				distance = remaining
			}
			changed = a.State.MoveWithTerrain(tank.ID, a.moveDirection, distance, a.terrain) == nil
		}
	}
	currentTankID := a.State.CurrentTankID
	gravityChanged, eliminated := a.State.ApplyGravityAndEliminate(a.terrain)
	changed = changed || gravityChanged
	if len(eliminated) > 0 && contains(eliminated, currentTankID) {
		a.moveDirection, a.aimDirection = "", ""
		if a.State.Phase != "finished" {
			a.State.TurnDeadlineMS = time.Now().Add(a.timeout).UnixMilli()
		}
	}
	if a.State.Phase == "finished" {
		a.moveDirection, a.aimDirection = "", ""
		a.Events <- Event{Type: "battle.player_eliminated", State: a.State}
		return
	}
	if a.aimDirection != "" {
		changed = a.State.Aim(a.State.CurrentTankID, a.aimDirection) == nil || changed
	}
	if time.Now().UnixMilli() >= a.State.TurnDeadlineMS {
		a.moveDirection, a.aimDirection = "", ""
		a.State.TimeoutTurn()
		a.State.TurnDeadlineMS = time.Now().Add(a.timeout).UnixMilli()
		a.Events <- Event{Type: "battle.turn_changed", State: a.State}
		return
	}
	if changed && (len(eliminated) > 0 || time.Since(a.lastBroadcast) >= 100*time.Millisecond) {
		a.lastBroadcast = time.Now()
		a.Events <- Event{Type: "battle.tank_state", State: a.State}
	}
}

func contains(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}
func (a *Actor) apply(c Command) {
	var e error
	var shot engine.Shot
	if a.State.IntroEndMS > 0 && time.Now().UnixMilli() < a.State.IntroEndMS {
		switch c.Type {
		case "pause", "resume", "leave", "disconnect_timeout", "move_stop", "aim_stop":
			// lifecycle and cleanup commands remain available during intro.
		default:
			e = engine.ErrIntroInProgress
			ev := Event{Type: "battle.tank_state", State: a.State, Error: e}
			if c.Reply != nil {
				c.Reply <- ev
			}
			a.Events <- ev
			return
		}
	}
	switch c.Type {
	case "pause":
		if !a.paused {
			a.paused = true
			a.pausedAt = time.Now()
		}
	case "resume":
		if a.paused {
			if !a.pausedAt.IsZero() {
				a.State.TurnDeadlineMS += time.Since(a.pausedAt).Milliseconds()
			}
			a.paused = false
			a.pausedAt = time.Time{}
		}
	case "move_start":
		if c.TankID != a.State.CurrentTankID || (c.Direction != "left" && c.Direction != "right") {
			e = engine.ErrInvalidCommand
		} else {
			a.moveDirection = c.Direction
		}
	case "move_stop":
		if c.TankID != a.State.CurrentTankID {
			e = engine.ErrNotYourTurn
		} else {
			a.moveDirection = ""
		}
	case "aim_start":
		if c.TankID != a.State.CurrentTankID || (c.Direction != "up" && c.Direction != "down") {
			e = engine.ErrInvalidCommand
		} else {
			a.aimDirection = c.Direction
		}
	case "aim_stop":
		if c.TankID != a.State.CurrentTankID {
			e = engine.ErrNotYourTurn
		} else {
			a.aimDirection = ""
		}
	case "select_weapon":
		e = a.State.SelectWeapon(c.TankID, c.Weapon)
	case "fire":
		shot, e = a.State.FireWithTerrain(c.TankID, c.Power, a.terrain)
	case "leave", "disconnect_timeout":
		e = a.State.Eliminate(c.TankID)
	}
	if c.Type == "fire" && e == nil {
		a.moveDirection, a.aimDirection = "", ""
		a.State.TurnDeadlineMS = time.Now().Add(a.timeout).UnixMilli()
	}
	ev := Event{Type: "battle.tank_state", State: a.State, Error: e}
	if c.Type == "fire" && e == nil {
		ev.Type = "battle.shot_resolved"
		ev.Shot = &shot
	} else if (c.Type == "leave" || c.Type == "disconnect_timeout") && e == nil {
		ev.Type = "battle.player_eliminated"
	}
	if c.Reply != nil {
		c.Reply <- ev
	}
	a.Events <- ev
}
