package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"guntanks-server/dao"
	"strings"
	"time"
)

type User struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	SessionID    string `json:"-"`
	PasswordHash string `json:"-"`
	Wins         int    `json:"wins"`
	Losses       int    `json:"losses"`
	Draws        int    `json:"draws"`
	GamesPlayed  int    `json:"games_played"`
}
type Auth struct {
	users    dao.Store
	cost     int
	secret   []byte
	tokenTTL time.Duration
}

func NewAuth(cost int, secret string, users dao.Store) *Auth {
	return &Auth{users: users, cost: cost, secret: []byte(secret), tokenTTL: 2 * time.Hour}
}
func (a *Auth) SetTokenTTL(ttl time.Duration) {
	if ttl > 0 {
		a.tokenTTL = ttl
	}
}
func (a *Auth) Token(u User) (string, error) {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": u.ID, "username": u.Username, "jti": hex.EncodeToString(b), "exp": time.Now().Add(a.tokenTTL).Unix()}).SignedString(a.secret)
}
func (a *Auth) Verify(token string) (User, error) {
	t, e := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("invalid signing method")
		}
		return a.secret, nil
	})
	if e != nil || !t.Valid {
		return User{}, errors.New("invalid token")
	}
	c, ok := t.Claims.(jwt.MapClaims)
	if !ok {
		return User{}, errors.New("invalid claims")
	}
	id, _ := c["sub"].(string)
	sessionID, _ := c["jti"].(string)
	u, err := a.users.GetUserByID(context.Background(), id)
	if err != nil {
		return User{}, errors.New("user not found")
	}
	result := fromRecord(u)
	result.SessionID = sessionID
	return result, nil
}
func (a *Auth) Register(username, password string) (User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if _, err := a.users.GetUserByUsername(context.Background(), username); err == nil {
		return User{}, bcrypt.ErrMismatchedHashAndPassword
	}
	h, e := bcrypt.GenerateFromPassword(passwordBytes(password), a.cost)
	if e != nil {
		return User{}, e
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	u := User{ID: "user_" + hex.EncodeToString(b), Username: username, PasswordHash: string(h)}
	err := a.users.CreateUser(context.Background(), toRecord(u))
	return u, err
}
func (a *Auth) Login(username, password string) (User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	rec, err := a.users.GetUserByUsername(context.Background(), username)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(rec.PasswordHash), passwordBytes(password)) != nil {
		return User{}, bcrypt.ErrMismatchedHashAndPassword
	}
	_ = a.users.TouchLogin(context.Background(), rec.UserID)
	return fromRecord(rec), nil
}

func passwordBytes(password string) []byte {
	if len([]byte(password)) <= 72 {
		return []byte(password)
	}
	digest := sha256.Sum256([]byte(password))
	return []byte(hex.EncodeToString(digest[:]))
}
func (a *Auth) UserByID(id string) (User, bool) {
	rec, err := a.users.GetUserByID(context.Background(), id)
	if err != nil {
		return User{}, false
	}
	return fromRecord(rec), true
}

func toRecord(u User) dao.UserRecord {
	now := time.Now()
	return dao.UserRecord{UserID: u.ID, Username: u.Username, PasswordHash: u.PasswordHash, Wins: u.Wins, Losses: u.Losses, Draws: u.Draws, GamesPlayed: u.GamesPlayed, CreatedAt: now, UpdatedAt: now}
}

func fromRecord(u dao.UserRecord) User {
	return User{ID: u.UserID, Username: u.Username, PasswordHash: u.PasswordHash, Wins: u.Wins, Losses: u.Losses, Draws: u.Draws, GamesPlayed: u.GamesPlayed}
}
