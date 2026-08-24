package engine

import (
	"errors"
	"math"
	"math/rand"
)

var ErrNotYourTurn = errors.New("not your turn")
var ErrInvalidCommand = errors.New("invalid command")

type Weapon string

const (
	Shell1 Weapon = "shell1"
	Shell2 Weapon = "shell2"
	SS     Weapon = "ss"
)

type Wind struct {
	Speed     int `json:"speed"`
	Direction int `json:"direction"`
}
type Tank struct {
	ID         string  `json:"id"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Angle      float64 `json:"angle"`
	Health     float64 `json:"health"`
	Alive      bool    `json:"alive"`
	Weapon     Weapon  `json:"weapon"`
	SSCooldown int     `json:"ss_cooldown"`
	Moved      float64 `json:"moved"`
	Facing     string  `json:"facing"`
	Delay      int     `json:"delay"`
}
type Terrain struct {
	Width, Height int
	Mask          []bool
}
type State struct {
	BattleID       string            `json:"battle_id"`
	Revision       uint64            `json:"revision"`
	EventSeq       uint64            `json:"event_seq"`
	Phase          string            `json:"phase"`
	Round          int               `json:"round"`
	TurnIndex      int               `json:"turn_index"`
	CurrentTankID  string            `json:"current_tank_id"`
	TurnDeadlineMS int64             `json:"turn_deadline_ms"`
	WinnerTankID   string            `json:"winner_tank_id,omitempty"`
	Result         string            `json:"result,omitempty"`
	Wind           Wind              `json:"wind"`
	Tanks          []Tank            `json:"tanks"`
	Seed           int64             `json:"seed"`
	TurnsCompleted int               `json:"turns_completed"`
	WindChanges    int               `json:"wind_changes"`
	PlayerResults  map[string]string `json:"player_results,omitempty"`
}
type Point struct {
	X   float64 `json:"x"`
	Y   float64 `json:"y"`
	TMS int     `json:"t_ms"`
}
type Impact struct {
	Kind string  `json:"kind"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}
type TerrainDestroyed struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Radius int     `json:"radius"`
}
type DamageResult struct {
	TankID      string  `json:"tank_id"`
	Amount      float64 `json:"amount"`
	HealthAfter float64 `json:"health_after"`
}
type Shot struct {
	ShooterTankID     string           `json:"shooter_tank_id"`
	Weapon            Weapon           `json:"weapon"`
	Power             float64          `json:"power"`
	DurationMS        int              `json:"duration_ms"`
	Trajectory        []Point          `json:"trajectory"`
	Impact            Impact           `json:"impact"`
	TerrainDestroyed  TerrainDestroyed `json:"terrain_destroyed"`
	Damages           []DamageResult   `json:"damages"`
	EliminatedTankIDs []string         `json:"eliminated_tank_ids"`
	Damage            float64          `json:"damage"`
}

func NewState(id string, players int, seed int64) State {
	r := rand.New(rand.NewSource(seed))
	spawns := []float64{100, 400, 700, 1070}
	s := State{BattleID: id, Phase: "playing", Round: 1, TurnIndex: 0, Seed: seed, Wind: Wind{Speed: r.Intn(26), Direction: r.Intn(360)}, PlayerResults: map[string]string{}}
	for i := 0; i < players; i++ {
		s.Tanks = append(s.Tanks, Tank{ID: "tank_" + string(rune('1'+i)), X: spawns[i], Y: 300, Angle: 0, Health: 1000, Alive: true, Weapon: Shell1, Facing: "right"})
	}
	s.CurrentTankID = s.Tanks[0].ID
	return s
}
func (s *State) Tank(id string) (*Tank, error) {
	for i := range s.Tanks {
		if s.Tanks[i].ID == id {
			return &s.Tanks[i], nil
		}
	}
	return nil, errors.New("tank not found")
}
func (s *State) Move(id, direction string, distance float64) error {
	return s.MoveWithTerrain(id, direction, distance, nil)
}
func (s *State) MoveWithTerrain(id, direction string, distance float64, terrain *TerrainMask) error {
	t, e := s.Tank(id)
	if e != nil {
		return e
	}
	if id != s.CurrentTankID {
		return ErrNotYourTurn
	}
	if distance < 0 || distance > 80 {
		return errors.New("invalid distance")
	}
	if direction != "left" && direction != "right" {
		return errors.New("invalid direction")
	}
	step := distance
	if direction == "left" {
		step = -distance
		t.Facing = "left"
	} else {
		t.Facing = "right"
	}
	nx := t.X + step
	if nx >= 0 && nx <= 1200-25 && (terrain == nil || !terrain.SolidAtRect(int(math.Round(nx)), int(math.Round(t.Y)), 25, 24)) {
		t.X = nx
		t.Y = settleTankY(t, terrain)
		t.Moved += distance
	}
	s.Revision++
	return nil
}
func settleTankY(t *Tank, terrain *TerrainMask) float64 {
	if terrain == nil {
		return t.Y
	}
	if terrain.SolidAtRect(int(math.Round(t.X)), int(math.Round(t.Y+30)), 25, 1) {
		for i := 0; i < 30 && terrain.SolidAtRect(int(math.Round(t.X)), int(math.Round(t.Y+29)), 25, 1); i++ {
			t.Y--
		}
		return t.Y
	}
	for i := 0; i < 6; i++ {
		if terrain.SolidAtRect(int(math.Round(t.X)), int(math.Round(t.Y+30)), 25, 1) {
			break
		}
		t.Y++
	}
	return t.Y
}
func SettleY(t *Tank, terrain *TerrainMask) float64 { return settleTankY(t, terrain) }

// ApplyGravityAndEliminate advances every living tank, then resolves all boundary
// losses as one state transition so a simultaneous fall cannot settle twice.
func (s *State) ApplyGravityAndEliminate(terrain *TerrainMask) (changed bool, eliminated []string) {
	if s.Phase == "finished" {
		return false, nil
	}
	for i := range s.Tanks {
		tank := &s.Tanks[i]
		if !tank.Alive {
			continue
		}
		if terrain != nil {
			oldY := tank.Y
			tank.Y = settleTankY(tank, terrain)
			changed = changed || tank.Y != oldY
		}
		if math.Abs(tank.X) > 1200 || math.Abs(tank.Y) > 650 {
			eliminated = append(eliminated, tank.ID)
		}
	}
	if len(eliminated) == 0 {
		if changed {
			s.Revision++
			s.EventSeq++
		}
		return changed, nil
	}
	currentEliminated := false
	for _, id := range eliminated {
		tank, _ := s.Tank(id)
		tank.Alive, tank.Health = false, 0
		currentEliminated = currentEliminated || id == s.CurrentTankID
	}
	s.Revision++
	s.EventSeq++
	s.finishIfDecided()
	if s.Phase != "finished" && currentEliminated {
		s.nextTurn()
	}
	return true, eliminated
}
func (s *State) Aim(id, direction string) error {
	t, e := s.Tank(id)
	if e != nil {
		return e
	}
	if id != s.CurrentTankID {
		return ErrNotYourTurn
	}
	if direction == "up" {
		t.Angle += 2
	} else if direction == "down" {
		t.Angle -= 2
	} else {
		return errors.New("invalid direction")
	}
	for t.Angle < 0 {
		t.Angle += 360
	}
	for t.Angle >= 360 {
		t.Angle -= 360
	}
	s.Revision++
	return nil
}
func (s *State) SelectWeapon(id string, w Weapon) error {
	t, e := s.Tank(id)
	if e != nil {
		return e
	}
	if id != s.CurrentTankID {
		return ErrNotYourTurn
	}
	if w != Shell1 && w != Shell2 && w != SS {
		return errors.New("invalid weapon")
	}
	if w == SS && t.SSCooldown > 0 {
		return errors.New("weapon cooldown")
	}
	t.Weapon = w
	s.Revision++
	return nil
}
func (s *State) Fire(id string, power float64) (Shot, error) {
	return s.FireWithTerrain(id, power, nil)
}
func (s *State) FireWithTerrain(id string, power float64, terrain *TerrainMask) (Shot, error) {
	t, e := s.Tank(id)
	if e != nil {
		return Shot{}, e
	}
	if id != s.CurrentTankID {
		return Shot{}, ErrNotYourTurn
	}
	if power < 0 || power > 100 {
		return Shot{}, errors.New("invalid power")
	}
	w := t.Weapon
	if w == SS && t.SSCooldown > 0 {
		return Shot{}, errors.New("weapon cooldown")
	}
	if w == SS {
		t.SSCooldown = 3
	}
	params := map[Weapon]struct {
		radius         int
		weight, damage float64
		delay          int
	}{Shell1: {8, .055, 130, 750}, Shell2: {7, .08, 240, 900}, SS: {10, .08, 350, 1300}}[w]
	t.Delay += params.delay
	t.SSCooldown--
	if w == SS {
		t.Weapon = Shell1
	}
	direction := 1.0
	x, y := t.X+25, t.Y-5
	if t.Facing == "left" {
		direction, x, y = -1, t.X-16, t.Y-10
	}
	xv := direction * power * .25 * math.Cos(t.Angle*math.Pi/180)
	yv := -power * .25 * math.Sin(t.Angle*math.Pi/180)
	points := make([]Point, 0, 512)
	impact := Impact{Kind: "out_of_bounds", X: x, Y: y}
	var hit *Tank
	for step, simTime := 0, 0.0; step < 4000; step, simTime = step+1, simTime+.5 {
		points = append(points, Point{X: x, Y: y, TMS: step * 16})
		for i := range s.Tanks {
			candidate := &s.Tanks[i]
			if candidate.Alive && math.Hypot((x+float64(params.radius))-(candidate.X+12.5), (y+float64(params.radius))-(candidate.Y+15)) < float64(params.radius)+12.5 {
				hit = candidate
				impact = Impact{Kind: "tank", X: x, Y: y}
				break
			}
		}
		if hit != nil {
			break
		}
		if terrain != nil && terrain.SolidAtRect(int(math.Round(x)), int(math.Round(y)), params.radius*2, params.radius*2) {
			impact = Impact{Kind: "terrain", X: x, Y: y}
			break
		}
		if x < -float64(params.radius) || x > 1200+float64(params.radius) || y < -200 || y > 650+float64(params.radius) {
			impact = Impact{Kind: "out_of_bounds", X: x, Y: y}
			break
		}
		x += xv + .02*float64(s.Wind.Speed)*math.Cos(float64(s.Wind.Direction)*math.Pi/180)*simTime
		y += yv + .5*params.weight*simTime*simTime - .02*float64(s.Wind.Speed)*math.Sin(float64(s.Wind.Direction)*math.Pi/180)*simTime
	}
	shot := Shot{ShooterTankID: id, Weapon: w, Power: power, DurationMS: 0, Trajectory: samplePoints(points, 240), Impact: impact, Damage: params.damage, Damages: []DamageResult{}, EliminatedTankIDs: []string{}}
	if len(points) > 0 {
		shot.DurationMS = points[len(points)-1].TMS
	}
	if hit != nil {
		hit.Health -= params.damage
		if hit.Health < 0 {
			hit.Health = 0
		}
		shot.Damages = append(shot.Damages, DamageResult{TankID: hit.ID, Amount: params.damage, HealthAfter: hit.Health})
		if hit.Health == 0 {
			hit.Alive = false
			shot.EliminatedTankIDs = append(shot.EliminatedTankIDs, hit.ID)
		}
	}
	shot.TerrainDestroyed = TerrainDestroyed{X: impact.X, Y: impact.Y, Radius: map[Weapon]int{Shell1: 48, Shell2: 35, SS: 70}[w]}
	if terrain != nil && impact.Kind != "out_of_bounds" {
		terrain.DestroyCircle(int(math.Round(impact.X)), int(math.Round(impact.Y)), shot.TerrainDestroyed.Radius)
	}
	s.EventSeq++
	s.Revision++
	s.nextTurn()
	s.finishIfDecided()
	return shot, nil
}
func samplePoints(points []Point, max int) []Point {
	if len(points) <= max {
		return points
	}
	out := make([]Point, 0, max)
	for i := 0; i < max; i++ {
		out = append(out, points[i*(len(points)-1)/(max-1)])
	}
	return out
}
func (s *State) Eliminate(id string) error {
	t, e := s.Tank(id)
	if e != nil {
		return e
	}
	if !t.Alive {
		return nil
	}
	t.Alive = false
	t.Health = 0
	s.EventSeq++
	s.Revision++
	if s.CurrentTankID == id {
		s.nextTurn()
	}
	s.finishIfDecided()
	return nil
}
func (s *State) TimeoutTurn() {
	if s.Phase == "finished" {
		return
	}
	s.EventSeq++
	s.Revision++
	s.nextTurn()
}
func (s *State) nextTurn() {
	for n := 0; n < len(s.Tanks); n++ {
		s.TurnIndex = (s.TurnIndex + 1) % len(s.Tanks)
		if s.Tanks[s.TurnIndex].Alive {
			s.CurrentTankID = s.Tanks[s.TurnIndex].ID
			s.TurnsCompleted++
			alive := 0
			for _, tank := range s.Tanks {
				if tank.Alive {
					alive++
				}
			}
			if alive > 0 && s.TurnsCompleted%alive == 0 {
				s.Round++
				if (s.Round-1)%3 == 0 {
					s.changeWind()
				}
			}
			s.Tanks[s.TurnIndex].Moved = 0
			return
		}
	}
}
func (s *State) changeWind() {
	s.WindChanges++
	r := rand.New(rand.NewSource(s.Seed + int64(s.WindChanges)*7919))
	s.Wind = Wind{Speed: r.Intn(26), Direction: r.Intn(360)}
}
func (s *State) finishIfDecided() {
	alive := 0
	winner := ""
	for _, t := range s.Tanks {
		if t.Alive {
			alive++
			winner = t.ID
		}
	}
	if alive == 1 {
		s.Phase = "finished"
		s.Result = "win"
		s.WinnerTankID = winner
		for _, t := range s.Tanks {
			if t.ID == winner {
				s.PlayerResults[t.ID] = "win"
			} else {
				s.PlayerResults[t.ID] = "loss"
			}
		}
	} else if alive == 0 {
		s.Phase = "finished"
		s.Result = "draw"
		s.CurrentTankID = ""
		for _, t := range s.Tanks {
			s.PlayerResults[t.ID] = "draw"
		}
	}
}
