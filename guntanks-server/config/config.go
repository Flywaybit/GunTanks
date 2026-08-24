package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	WebAddr, StaticDir, MongoURI, MongoDB, RedisAddr, RedisPassword, JWTSecret             string
	Environment                                                                            string
	BcryptCost, TurnTimeoutSeconds, ReconnectGraceSeconds, BattleTickHz, MaxWSMessageBytes int
	RedisOnlineTTLSeconds                                                                  int
	AccessTokenTTLSeconds                                                                  int
	TerrainSnapshotEventInterval, TerrainSnapshotSeconds                                   int
	ShutdownTimeoutSeconds, WebSocketShutdownTimeoutSeconds, WebSocketWriteTimeoutSeconds int
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func envInt(key string, fallback int) int {
	v, err := strconv.Atoi(env(key, fmt.Sprint(fallback)))
	if err != nil {
		return fallback
	}
	return v
}
func Load() Config {
	return Config{
		WebAddr: env("WEB_ADDR", ":8889"), StaticDir: env("STATIC_DIR", "../guntanks-client"),
		Environment: env("APP_ENV", "development"),
		MongoURI:    env("MONGO_URI", "mongodb://127.0.0.1:27017"), MongoDB: env("MONGO_DB", "guntanks"),
		RedisAddr: env("REDIS_ADDR", "127.0.0.1:6379"), RedisPassword: os.Getenv("REDIS_PASSWORD"),
		JWTSecret: env("JWT_SECRET", "development-only-change-me"), BcryptCost: envInt("BCRYPT_COST", 12),
		TurnTimeoutSeconds: envInt("TURN_TIMEOUT_SECONDS", 30), ReconnectGraceSeconds: envInt("RECONNECT_GRACE_SECONDS", 60),
		BattleTickHz: envInt("BATTLE_TICK_HZ", 60), MaxWSMessageBytes: envInt("MAX_WS_MESSAGE_BYTES", 262144),
		RedisOnlineTTLSeconds:        envInt("REDIS_ONLINE_TTL_SECONDS", 300),
		AccessTokenTTLSeconds:        envInt("ACCESS_TOKEN_TTL_SECONDS", 7200),
		TerrainSnapshotEventInterval: envInt("TERRAIN_SNAPSHOT_EVENT_INTERVAL", 10), TerrainSnapshotSeconds: envInt("TERRAIN_SNAPSHOT_SECONDS", 30),
		ShutdownTimeoutSeconds: envInt("SHUTDOWN_TIMEOUT_SECONDS", 5), WebSocketShutdownTimeoutSeconds: envInt("WEBSOCKET_SHUTDOWN_TIMEOUT_SECONDS", 2), WebSocketWriteTimeoutSeconds: envInt("WEBSOCKET_WRITE_TIMEOUT_SECONDS", 1),
	}
}
func (c Config) Validate() error {
	if c.BcryptCost < 10 || c.BcryptCost > 31 {
		return fmt.Errorf("BCRYPT_COST must be 10..31")
	}
	if c.TurnTimeoutSeconds <= 0 || c.BattleTickHz <= 0 || c.AccessTokenTTLSeconds <= 0 {
		return fmt.Errorf("timeouts and tick rate must be positive")
	}
	if c.Environment == "production" {
		if c.JWTSecret == "" || c.JWTSecret == "development-only-change-me" {
			return fmt.Errorf("JWT_SECRET is required in production")
		}
		if c.MongoURI == "" || c.RedisAddr == "" {
			return fmt.Errorf("MongoDB and Redis configuration is required in production")
		}
	}
	return nil
}
