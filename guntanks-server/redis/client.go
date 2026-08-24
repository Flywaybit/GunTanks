package redis

import (
	"context"
	goredis "github.com/redis/go-redis/v9"
	"time"
)

type Client struct{ RDB *goredis.Client }

func NewClient(addr, password string, db int) *Client {
	return &Client{RDB: goredis.NewClient(&goredis.Options{Addr: addr, Password: password, DB: db})}
}
func (c *Client) Online(ctx context.Context, user string, ttl time.Duration) error {
	return c.RDB.Set(ctx, "guntanks:online:user:"+user, "1", ttl).Err()
}
func (c *Client) Acquire(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	return c.RDB.SetNX(ctx, key, value, ttl).Result()
}
func (c *Client) Ping(ctx context.Context) error { return c.RDB.Ping(ctx).Err() }
func (c *Client) AcquireOnline(ctx context.Context, user, generation string, ttl time.Duration) (bool, error) {
	return c.RDB.SetNX(ctx, "guntanks:online:user:"+user, generation, ttl).Result()
}
func (c *Client) RefreshOnline(ctx context.Context, user, generation string, ttl time.Duration) (bool, error) {
	key := "guntanks:online:user:" + user
	result, err := c.RDB.Eval(ctx, `if redis.call('GET',KEYS[1]) == ARGV[1] then return redis.call('EXPIRE',KEYS[1],ARGV[2]) else return 0 end`, []string{key}, generation, int(ttl.Seconds())).Int()
	return result == 1, err
}
func (c *Client) ReleaseOnline(ctx context.Context, user, generation string) error {
	key := "guntanks:online:user:" + user
	return c.RDB.Eval(ctx, `if redis.call('GET',KEYS[1]) == ARGV[1] then return redis.call('DEL',KEYS[1]) else return 0 end`, []string{key}, generation).Err()
}
func (c *Client) SetReconnect(ctx context.Context, user, battle string, ttl time.Duration) error {
	return c.RDB.Set(ctx, "guntanks:reconnect:user:"+user, battle, ttl).Err()
}
func (c *Client) ClearReconnect(ctx context.Context, user string) error {
	return c.RDB.Del(ctx, "guntanks:reconnect:user:"+user).Err()
}
func (c *Client) ReconnectBattle(ctx context.Context, user string) (string, bool, error) {
	value, err := c.RDB.Get(ctx, "guntanks:reconnect:user:"+user).Result()
	if err == goredis.Nil {
		return "", false, nil
	}
	return value, err == nil, err
}
func (c *Client) ReplaceOnline(ctx context.Context, user, generation string, ttl time.Duration) error {
	return c.RDB.Set(ctx, "guntanks:online:user:"+user, generation, ttl).Err()
}
func (c *Client) ReleaseAll(ctx context.Context) error {
	for _, pattern := range []string{"guntanks:online:user:*", "guntanks:reconnect:user:*"} {
		var cursor uint64
		for {
			keys, next, err := c.RDB.Scan(ctx, cursor, pattern, 200).Result()
			if err != nil {
				return err
			}
			if len(keys) > 0 {
				if err := c.RDB.Del(ctx, keys...).Err(); err != nil {
					return err
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	return nil
}
func (c *Client) Close() error { return c.RDB.Close() }
