// mint_token generates a short-lived JWT for an existing user, so the
// nexus_e2e.sh script can run without needing the user to copy-paste a
// token from the browser. Pull the secret from the live container's env
// to stay in sync with what the backend will accept.
//
// Usage:
//   JWT_SECRET=... MONGODB_URI=mongodb://localhost:27017/claraverse \
//     go run ./cmd/mint_token/
//
// Picks the first user in the users collection. Prints the token to stdout
// — pipe it into AUTH_TOKEN for the e2e script.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"claraverse/pkg/auth"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		log.Fatal("JWT_SECRET env var required")
	}
	uri := os.Getenv("MONGODB_URI")
	if uri == "" {
		uri = "mongodb://localhost:27017/claraverse"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		log.Fatalf("mongo: %v", err)
	}
	defer client.Disconnect(context.Background())

	var user struct {
		ID    interface{} `bson:"_id"`
		Email string      `bson:"email"`
		Role  string      `bson:"role"`
	}
	if err := client.Database("claraverse").Collection("users").
		FindOne(ctx, bson.M{}).Decode(&user); err != nil {
		log.Fatalf("no user found: %v", err)
	}

	role := user.Role
	if role == "" {
		role = "user"
	}

	jwtAuth, err := auth.NewLocalJWTAuth(secret, 30*time.Minute, 24*time.Hour)
	if err != nil {
		log.Fatalf("jwt init: %v", err)
	}

	// _id can be either ObjectID (.Hex()) or string depending on insertion path.
	var userID string
	switch v := user.ID.(type) {
	case interface{ Hex() string }:
		userID = v.Hex()
	default:
		userID = fmt.Sprintf("%v", v)
	}

	access, _, err := jwtAuth.GenerateTokens(userID, user.Email, role)
	if err != nil {
		log.Fatalf("sign: %v", err)
	}

	fmt.Print(access)
}
