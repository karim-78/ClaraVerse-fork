package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Routine represents a scheduled AI agent task in Dobby's Claw
type Routine struct {
	ID             primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID         string             `bson:"userId" json:"userId"`
	Name           string             `bson:"name" json:"name"`
	Prompt         string             `bson:"prompt" json:"prompt"`
	CronExpression string             `bson:"cronExpression" json:"cronExpression"`
	Timezone       string             `bson:"timezone" json:"timezone"`
	Enabled        bool               `bson:"enabled" json:"enabled"`
	DeliveryMethod string             `bson:"deliveryMethod" json:"deliveryMethod"` // "telegram" | "store"
	ModelID        string             `bson:"modelId,omitempty" json:"modelId,omitempty"`
	EnabledTools   []string           `bson:"enabledTools,omitempty" json:"enabledTools,omitempty"`
	Template       string             `bson:"template" json:"template"` // template ID or "custom"

	// Stats
	TotalRuns      int64      `bson:"totalRuns" json:"totalRuns"`
	SuccessfulRuns int64      `bson:"successfulRuns" json:"successfulRuns"`
	FailedRuns     int64      `bson:"failedRuns" json:"failedRuns"`
	LastRunAt      *time.Time `bson:"lastRunAt,omitempty" json:"lastRunAt,omitempty"`
	NextRunAt      *time.Time `bson:"nextRunAt,omitempty" json:"nextRunAt,omitempty"`
	LastResult     string     `bson:"lastResult,omitempty" json:"lastResult,omitempty"`

	// Timestamps
	CreatedAt time.Time `bson:"createdAt" json:"createdAt"`
	UpdatedAt time.Time `bson:"updatedAt" json:"updatedAt"`
}

// CreateRoutineRequest represents a request to create a routine
type CreateRoutineRequest struct {
	Name           string   `json:"name" validate:"required"`
	Prompt         string   `json:"prompt" validate:"required"`
	CronExpression string   `json:"cronExpression" validate:"required"`
	Timezone       string   `json:"timezone" validate:"required"`
	DeliveryMethod string   `json:"deliveryMethod" validate:"required"`
	ModelID        string   `json:"modelId,omitempty"`
	EnabledTools   []string `json:"enabledTools,omitempty"`
	Template       string   `json:"template,omitempty"`
}

// UpdateRoutineRequest represents a request to update a routine
type UpdateRoutineRequest struct {
	Name           *string  `json:"name,omitempty"`
	Prompt         *string  `json:"prompt,omitempty"`
	CronExpression *string  `json:"cronExpression,omitempty"`
	Timezone       *string  `json:"timezone,omitempty"`
	Enabled        *bool    `json:"enabled,omitempty"`
	DeliveryMethod *string  `json:"deliveryMethod,omitempty"`
	ModelID        *string  `json:"modelId,omitempty"`
	EnabledTools   []string `json:"enabledTools,omitempty"`
}

// TestRoutineRequest represents a request to test-run a routine without saving
type TestRoutineRequest struct {
	Name         string   `json:"name"`
	Prompt       string   `json:"prompt" validate:"required"`
	ModelID      string   `json:"modelId,omitempty"`
	EnabledTools []string `json:"enabledTools,omitempty"`
}
