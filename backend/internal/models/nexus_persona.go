package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// PersonaFact represents a single personality/character trait for Dobby
type PersonaFact struct {
	ID              primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID          string             `bson:"userId" json:"user_id"`
	Category        string             `bson:"category" json:"category"` // "personality","communication","expertise","boundaries"
	Content         string             `bson:"content" json:"content"`
	Confidence      float64            `bson:"confidence" json:"confidence"` // 0.0-1.0
	Source          string             `bson:"source" json:"source"`         // "user_explicit","inferred","default"
	ReinforcedCount int                `bson:"reinforcedCount" json:"reinforced_count"`
	CreatedAt       time.Time          `bson:"createdAt" json:"created_at"`
	UpdatedAt       time.Time          `bson:"updatedAt" json:"updated_at"`
}

// DefaultPersonaFacts returns the initial persona facts for a new user
func DefaultPersonaFacts(userID string) []PersonaFact {
	now := time.Now()
	return []PersonaFact{
		{
			ID:         primitive.NewObjectID(),
			UserID:     userID,
			Category:   "personality",
			Content:    "Dobby is helpful, proactive, and action-oriented",
			Confidence: 1.0,
			Source:     "default",
			CreatedAt:  now,
			UpdatedAt:  now,
		},
		{
			ID:         primitive.NewObjectID(),
			UserID:     userID,
			Category:   "communication",
			Content:    "Dobby communicates concisely and clearly, avoiding unnecessary verbosity",
			Confidence: 1.0,
			Source:     "default",
			CreatedAt:  now,
			UpdatedAt:  now,
		},
		{
			ID:         primitive.NewObjectID(),
			UserID:     userID,
			Category:   "boundaries",
			Content:    "Dobby stays focused on the task at hand and asks for clarification when needed",
			Confidence: 1.0,
			Source:     "default",
			CreatedAt:  now,
			UpdatedAt:  now,
		},
	}
}
