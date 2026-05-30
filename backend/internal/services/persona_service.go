package services

import (
	"context"
	"fmt"
	"strings"
	"time"

	"claraverse/internal/database"
	"claraverse/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// PersonaService manages Dobby's personality/character facts per user
type PersonaService struct {
	collection *mongo.Collection
}

// NewPersonaService creates a new persona service
func NewPersonaService(mongodb *database.MongoDB) *PersonaService {
	return &PersonaService{
		collection: mongodb.Collection(database.CollectionNexusPersona),
	}
}

// GetAll returns all persona facts for a user
func (s *PersonaService) GetAll(ctx context.Context, userID string) ([]models.PersonaFact, error) {
	cursor, err := s.collection.Find(ctx, bson.M{"userId": userID})
	if err != nil {
		return nil, fmt.Errorf("failed to get persona facts: %w", err)
	}
	defer cursor.Close(ctx)

	var facts []models.PersonaFact
	if err := cursor.All(ctx, &facts); err != nil {
		return nil, fmt.Errorf("failed to decode persona facts: %w", err)
	}
	return facts, nil
}

// GetByCategory returns persona facts for a specific category
func (s *PersonaService) GetByCategory(ctx context.Context, userID string, category string) ([]models.PersonaFact, error) {
	cursor, err := s.collection.Find(ctx, bson.M{
		"userId":   userID,
		"category": category,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get persona facts by category: %w", err)
	}
	defer cursor.Close(ctx)

	var facts []models.PersonaFact
	if err := cursor.All(ctx, &facts); err != nil {
		return nil, fmt.Errorf("failed to decode persona facts: %w", err)
	}
	return facts, nil
}

// GetOrCreateDefaults returns existing facts or creates defaults for a new user
func (s *PersonaService) GetOrCreateDefaults(ctx context.Context, userID string) ([]models.PersonaFact, error) {
	facts, err := s.GetAll(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(facts) > 0 {
		return facts, nil
	}

	// Seed defaults
	defaults := models.DefaultPersonaFacts(userID)
	docs := make([]interface{}, len(defaults))
	for i, f := range defaults {
		docs[i] = f
	}

	_, err = s.collection.InsertMany(ctx, docs)
	if err != nil {
		return nil, fmt.Errorf("failed to seed default persona: %w", err)
	}

	return defaults, nil
}

// Create adds a new persona fact
func (s *PersonaService) Create(ctx context.Context, fact *models.PersonaFact) error {
	now := time.Now()
	fact.CreatedAt = now
	fact.UpdatedAt = now
	if fact.Confidence == 0 {
		fact.Confidence = 0.5
	}

	result, err := s.collection.InsertOne(ctx, fact)
	if err != nil {
		return fmt.Errorf("failed to create persona fact: %w", err)
	}

	fact.ID = result.InsertedID.(primitive.ObjectID)
	return nil
}

// Update modifies an existing persona fact
func (s *PersonaService) Update(ctx context.Context, userID string, factID primitive.ObjectID, content string, category string) error {
	update := bson.M{
		"$set": bson.M{
			"updatedAt": time.Now(),
		},
	}
	if content != "" {
		update["$set"].(bson.M)["content"] = content
	}
	if category != "" {
		update["$set"].(bson.M)["category"] = category
	}

	result, err := s.collection.UpdateOne(ctx, bson.M{
		"_id":    factID,
		"userId": userID,
	}, update)
	if err != nil {
		return fmt.Errorf("failed to update persona fact: %w", err)
	}
	if result.MatchedCount == 0 {
		return fmt.Errorf("persona fact not found")
	}
	return nil
}

// Delete removes a persona fact
func (s *PersonaService) Delete(ctx context.Context, userID string, factID primitive.ObjectID) error {
	result, err := s.collection.DeleteOne(ctx, bson.M{
		"_id":    factID,
		"userId": userID,
	})
	if err != nil {
		return fmt.Errorf("failed to delete persona fact: %w", err)
	}
	if result.DeletedCount == 0 {
		return fmt.Errorf("persona fact not found")
	}
	return nil
}

// Reinforce increments the reinforcement counter and boosts confidence
func (s *PersonaService) Reinforce(ctx context.Context, userID string, factID primitive.ObjectID) error {
	_, err := s.collection.UpdateOne(ctx, bson.M{
		"_id":    factID,
		"userId": userID,
	}, bson.M{
		"$inc": bson.M{"reinforcedCount": 1},
		"$set": bson.M{"updatedAt": time.Now()},
		"$min": bson.M{"confidence": 1.0}, // Cap at 1.0
	})
	if err != nil {
		return fmt.Errorf("failed to reinforce persona fact: %w", err)
	}
	return nil
}

// BuildSystemPrompt assembles persona facts into a system prompt section
func (s *PersonaService) BuildSystemPrompt(ctx context.Context, userID string) (string, error) {
	facts, err := s.GetOrCreateDefaults(ctx, userID)
	if err != nil {
		return "", err
	}

	if len(facts) == 0 {
		return "", nil
	}

	var sb strings.Builder
	sb.WriteString("## Dobby's Persona\n\n")

	categories := map[string][]string{}
	for _, f := range facts {
		if f.Confidence < 0.3 {
			continue // Skip low-confidence facts
		}
		categories[f.Category] = append(categories[f.Category], f.Content)
	}

	for category, contents := range categories {
		sb.WriteString(fmt.Sprintf("### %s\n", strings.Title(category)))
		for _, c := range contents {
			sb.WriteString(fmt.Sprintf("- %s\n", c))
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}
