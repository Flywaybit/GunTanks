package engine

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"image/png"
	"io"
	"os"
)

type TerrainMask struct {
	Width, Height int
	Solid         []bool
}
type TerrainSnapshot struct {
	SnapshotSeq   uint64 `json:"snapshot_seq"`
	Width, Height int    `json:"width,height"`
	Encoding      string `json:"encoding"`
	Data          []byte `json:"data"`
	Checksum      string `json:"checksum"`
}

func NewTerrain(w, h int) *TerrainMask {
	return &TerrainMask{Width: w, Height: h, Solid: make([]bool, w*h)}
}
func LoadTerrainPNG(path string, worldWidth, worldHeight, offsetX, offsetY int) (*TerrainMask, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	mask := NewTerrain(worldWidth, worldHeight)
	bounds := img.Bounds()
	for sy := bounds.Min.Y; sy < bounds.Max.Y; sy++ {
		for sx := bounds.Min.X; sx < bounds.Max.X; sx++ {
			_, _, _, alpha := img.At(sx, sy).RGBA()
			if alpha != 0 {
				x, y := offsetX+sx-bounds.Min.X, offsetY+sy-bounds.Min.Y
				if i := mask.Index(x, y); i >= 0 {
					mask.Solid[i] = true
				}
			}
		}
	}
	sum := sha256.Sum256(data)
	return mask, hex.EncodeToString(sum[:]), nil
}
func (t *TerrainMask) Index(x, y int) int {
	if x < 0 || y < 0 || x >= t.Width || y >= t.Height {
		return -1
	}
	return y*t.Width + x
}
func (t *TerrainMask) SolidAtRect(x, y, width, height int) bool {
	for py := y; py < y+height; py++ {
		for px := x; px < x+width; px++ {
			if i := t.Index(px, py); i >= 0 && t.Solid[i] {
				return true
			}
		}
	}
	return false
}
func (t *TerrainMask) DestroyCircle(cx, cy, r int) {
	r2 := r * r
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r2 {
				if i := t.Index(x, y); i >= 0 {
					t.Solid[i] = false
				}
			}
		}
	}
}
func (t *TerrainMask) Snapshot(seq uint64) (TerrainSnapshot, error) {
	raw := make([]byte, (len(t.Solid)+7)/8)
	for i, v := range t.Solid {
		if v {
			raw[i/8] |= 1 << uint(i%8)
		}
	}
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	if _, e := z.Write(raw); e != nil {
		return TerrainSnapshot{}, e
	}
	if e := z.Close(); e != nil {
		return TerrainSnapshot{}, e
	}
	sum := sha256.Sum256(raw)
	return TerrainSnapshot{SnapshotSeq: seq, Width: t.Width, Height: t.Height, Encoding: "gzip-bitset-v1", Data: b.Bytes(), Checksum: hex.EncodeToString(sum[:])}, nil
}
func VerifySnapshot(s TerrainSnapshot) (bool, error) {
	z, e := gzip.NewReader(bytes.NewReader(s.Data))
	if e != nil {
		return false, e
	}
	raw, e := io.ReadAll(z)
	if e != nil {
		return false, e
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]) == s.Checksum, nil
}
