package dao

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type MongoStore struct{ db *mongo.Database; client *mongo.Client }

func NewMongoStore(client *mongo.Client, dbName string) *MongoStore {
	return &MongoStore{db: client.Database(dbName), client: client}
}
func (m *MongoStore) Close(ctx context.Context) error { return m.client.Disconnect(ctx) }

func (m *MongoStore) EnsureIndexes(ctx context.Context) error {
	models := map[string][]mongo.IndexModel{
		"users": {
			{Keys: bson.D{{Key: "username", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "user_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		},
		"battles": {
			{Keys: bson.D{{Key: "battle_id", Value: 1}}, Options: options.Index().SetUnique(true)},
			{Keys: bson.D{{Key: "players.user_id", Value: 1}, {Key: "created_at", Value: -1}}},
			{Keys: bson.D{{Key: "status", Value: 1}, {Key: "updated_at", Value: -1}}},
		},
		"battle_events": {
			{Keys: bson.D{{Key: "battle_id", Value: 1}, {Key: "seq", Value: 1}}, Options: options.Index().SetUnique(true)},
		},
		"terrain_snapshots": {
			{Keys: bson.D{{Key: "battle_id", Value: 1}, {Key: "snapshot_seq", Value: 1}}, Options: options.Index().SetUnique(true)},
		},
	}
	for collection, indexes := range models {
		if _, err := m.db.Collection(collection).Indexes().CreateMany(ctx, indexes); err != nil {
			return err
		}
	}
	return nil
}

func (m *MongoStore) CreateUser(ctx context.Context, u UserRecord) error {
	now := time.Now()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	u.UpdatedAt = now
	_, err := m.db.Collection("users").InsertOne(ctx, u)
	if mongo.IsDuplicateKeyError(err) {
		return ErrDuplicate
	}
	return err
}

func (m *MongoStore) GetUserByUsername(ctx context.Context, username string) (UserRecord, error) {
	var u UserRecord
	err := m.db.Collection("users").FindOne(ctx, bson.M{"username": username}).Decode(&u)
	if err == mongo.ErrNoDocuments {
		return UserRecord{}, ErrNotFound
	}
	return u, err
}

func (m *MongoStore) GetUserByID(ctx context.Context, id string) (UserRecord, error) {
	var u UserRecord
	err := m.db.Collection("users").FindOne(ctx, bson.M{"user_id": id}).Decode(&u)
	if err == mongo.ErrNoDocuments {
		return UserRecord{}, ErrNotFound
	}
	return u, err
}

func (m *MongoStore) TouchLogin(ctx context.Context, id string) error {
	_, err := m.db.Collection("users").UpdateOne(ctx, bson.M{"user_id": id}, bson.M{"$set": bson.M{"last_login_at": time.Now(), "updated_at": time.Now()}})
	return err
}

func (m *MongoStore) SaveBattle(ctx context.Context, b BattleRecord) error {
	if b.CreatedAt.IsZero() {
		b.CreatedAt = time.Now()
	}
	b.UpdatedAt = time.Now()
	_, err := m.db.Collection("battles").ReplaceOne(ctx, bson.M{"battle_id": b.BattleID}, b, options.Replace().SetUpsert(true))
	return err
}

func (m *MongoStore) GetBattle(ctx context.Context, id string) (BattleRecord, error) {
	var v BattleRecord
	err := m.db.Collection("battles").FindOne(ctx, bson.M{"battle_id": id}).Decode(&v)
	return v, err
}

func (m *MongoStore) ListBattlesForUser(ctx context.Context, userID string, limit int) ([]BattleRecord, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	cur, err := m.db.Collection("battles").Find(ctx, bson.M{"players.user_id": userID}, options.Find().SetLimit(int64(limit)).SetSort(bson.D{{Key: "created_at", Value: -1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []BattleRecord
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (m *MongoStore) AppendEvent(ctx context.Context, e EventRecord) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	_, err := m.db.Collection("battle_events").InsertOne(ctx, e)
	if mongo.IsDuplicateKeyError(err) {
		return nil
	}
	return err
}

func (m *MongoStore) ListEvents(ctx context.Context, id string, afterSeq uint64) ([]EventRecord, error) {
	cur, err := m.db.Collection("battle_events").Find(ctx, bson.M{"battle_id": id, "seq": bson.M{"$gt": afterSeq}}, options.Find().SetSort(bson.D{{Key: "seq", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []EventRecord
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (m *MongoStore) SaveTerrainSnapshot(ctx context.Context, snap TerrainSnapshotRecord) error {
	if snap.CreatedAt.IsZero() {
		snap.CreatedAt = time.Now()
	}
	_, err := m.db.Collection("terrain_snapshots").ReplaceOne(ctx, bson.M{"battle_id": snap.BattleID, "snapshot_seq": snap.SnapshotSeq}, snap, options.Replace().SetUpsert(true))
	return err
}

func (m *MongoStore) GetLatestTerrainSnapshot(ctx context.Context, battleID string) (TerrainSnapshotRecord, error) {
	var snap TerrainSnapshotRecord
	err := m.db.Collection("terrain_snapshots").FindOne(ctx, bson.M{"battle_id": battleID}, options.FindOne().SetSort(bson.D{{Key: "snapshot_seq", Value: -1}})).Decode(&snap)
	return snap, err
}

func (m *MongoStore) SettleBattle(ctx context.Context, b BattleRecord) (bool, error) {
	var current BattleRecord
	if err := m.db.Collection("battles").FindOne(ctx, bson.M{"battle_id": b.BattleID}).Decode(&current); err != nil {
		return false, err
	}
	if current.Settled {
		return false, nil
	}
	for _, player := range b.Players {
		inc := bson.M{"games_played": 1}
		switch player.Result {
		case "win":
			inc["wins"] = 1
		case "loss":
			inc["losses"] = 1
		case "draw":
			inc["draws"] = 1
		}
		filter := bson.M{"user_id": player.UserID, "settled_battles": bson.M{"$ne": b.BattleID}}
		update := bson.M{"$inc": inc, "$addToSet": bson.M{"settled_battles": b.BattleID}, "$set": bson.M{"updated_at": time.Now()}}
		if _, err := m.db.Collection("users").UpdateOne(ctx, filter, update); err != nil {
			return false, err
		}
	}
	b.Settled = true
	b.UpdatedAt = time.Now()
	if _, err := m.db.Collection("battles").ReplaceOne(ctx, bson.M{"battle_id": b.BattleID}, b); err != nil {
		return false, err
	}
	return true, nil
}
