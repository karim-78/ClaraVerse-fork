package services

import (
	"claraverse/internal/database"
	"claraverse/internal/models"
	"claraverse/internal/tools"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	cache "github.com/patrickmn/go-cache"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// SkillService manages AI skills (prompt+tool bundles)
type SkillService struct {
	mongoDB        *database.MongoDB
	toolRegistry   *tools.Registry
	communityCache *cache.Cache
}

// NewSkillService creates a new skill service
func NewSkillService(mongoDB *database.MongoDB, toolRegistry *tools.Registry) *SkillService {
	return &SkillService{
		mongoDB:        mongoDB,
		toolRegistry:   toolRegistry,
		communityCache: cache.New(15*time.Minute, 30*time.Minute),
	}
}

// CommunitySkillEntry is a lightweight skill listing from a GitHub repo
type CommunitySkillEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	RepoURL     string `json:"repo_url"`
	RawURL      string `json:"raw_url"`
	Author      string `json:"author"`
	License     string `json:"license"`
	Category    string `json:"category"`
	Icon        string `json:"icon"`
}

// EnsureIndexes creates required indexes for skill collections
func (s *SkillService) EnsureIndexes(ctx context.Context) error {
	skillsColl := s.mongoDB.Collection(database.CollectionSkills)

	skillIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "is_builtin", Value: 1}, {Key: "category", Value: 1}},
			Options: options.Index().SetName("idx_builtin_category"),
		},
		{
			Keys:    bson.D{{Key: "author_id", Value: 1}},
			Options: options.Index().SetName("idx_author"),
		},
		{
			Keys:    bson.D{{Key: "name", Value: 1}, {Key: "is_builtin", Value: 1}},
			Options: options.Index().SetName("idx_name_builtin").SetUnique(true),
		},
	}

	_, err := skillsColl.Indexes().CreateMany(ctx, skillIndexes)
	if err != nil {
		return fmt.Errorf("failed to create skills indexes: %w", err)
	}

	userSkillsColl := s.mongoDB.Collection(database.CollectionUserSkills)

	userSkillIndexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "user_id", Value: 1}, {Key: "skill_id", Value: 1}},
			Options: options.Index().SetName("idx_user_skill").SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "user_id", Value: 1}, {Key: "enabled", Value: 1}},
			Options: options.Index().SetName("idx_user_enabled"),
		},
	}

	_, err = userSkillsColl.Indexes().CreateMany(ctx, userSkillIndexes)
	if err != nil {
		return fmt.Errorf("failed to create user_skills indexes: %w", err)
	}

	return nil
}

// SeedBuiltinSkills upserts all built-in skills into the database
func (s *SkillService) SeedBuiltinSkills(ctx context.Context) error {
	coll := s.mongoDB.Collection(database.CollectionSkills)
	builtins := getBuiltinSkills()

	for _, skill := range builtins {
		filter := bson.M{"name": skill.Name, "is_builtin": true}
		update := bson.M{
			"$set": bson.M{
				"description":       skill.Description,
				"icon":              skill.Icon,
				"category":          skill.Category,
				"system_prompt":     skill.SystemPrompt,
				"required_tools":    skill.RequiredTools,
				"preferred_servers": skill.PreferredServers,
				"keywords":          skill.Keywords,
				"trigger_patterns":  skill.TriggerPatterns,
				"mode":              skill.Mode,
				"is_builtin":        true,
				"version":           skill.Version,
				"updated_at":        time.Now(),
			},
			"$setOnInsert": bson.M{
				"created_at": time.Now(),
			},
		}
		opts := options.Update().SetUpsert(true)
		_, err := coll.UpdateOne(ctx, filter, update, opts)
		if err != nil {
			log.Printf("⚠️ Failed to upsert built-in skill %s: %v", skill.Name, err)
		}
	}

	log.Printf("✅ Seeded %d built-in skills", len(builtins))
	return nil
}

// ListSkills returns all skills, optionally filtered by category
func (s *SkillService) ListSkills(ctx context.Context, category string) ([]models.Skill, error) {
	coll := s.mongoDB.Collection(database.CollectionSkills)

	filter := bson.M{}
	if category != "" {
		filter["category"] = category
	}

	opts := options.Find().SetSort(bson.D{{Key: "category", Value: 1}, {Key: "name", Value: 1}})
	cursor, err := coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list skills: %w", err)
	}
	defer cursor.Close(ctx)

	var skills []models.Skill
	if err := cursor.All(ctx, &skills); err != nil {
		return nil, fmt.Errorf("failed to decode skills: %w", err)
	}

	return skills, nil
}

// GetSkill returns a skill by ID
func (s *SkillService) GetSkill(ctx context.Context, id string) (*models.Skill, error) {
	coll := s.mongoDB.Collection(database.CollectionSkills)

	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, fmt.Errorf("invalid skill ID: %w", err)
	}

	var skill models.Skill
	err = coll.FindOne(ctx, bson.M{"_id": objID}).Decode(&skill)
	if err != nil {
		return nil, fmt.Errorf("failed to get skill: %w", err)
	}

	return &skill, nil
}

// CreateSkill creates a new custom skill
func (s *SkillService) CreateSkill(ctx context.Context, skill *models.Skill) error {
	coll := s.mongoDB.Collection(database.CollectionSkills)

	skill.ID = primitive.NewObjectID()
	skill.IsBuiltin = false
	skill.CreatedAt = time.Now()
	skill.UpdatedAt = time.Now()
	if skill.Version == "" {
		skill.Version = "1.0.0"
	}
	if skill.Mode == "" {
		skill.Mode = "auto"
	}

	_, err := coll.InsertOne(ctx, skill)
	if err != nil {
		return fmt.Errorf("failed to create skill: %w", err)
	}

	return nil
}

// UpdateSkill updates an existing skill
func (s *SkillService) UpdateSkill(ctx context.Context, id string, skill *models.Skill) error {
	coll := s.mongoDB.Collection(database.CollectionSkills)

	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid skill ID: %w", err)
	}

	update := bson.M{
		"$set": bson.M{
			"name":              skill.Name,
			"description":       skill.Description,
			"icon":              skill.Icon,
			"category":          skill.Category,
			"system_prompt":     skill.SystemPrompt,
			"required_tools":    skill.RequiredTools,
			"preferred_servers": skill.PreferredServers,
			"keywords":          skill.Keywords,
			"trigger_patterns":  skill.TriggerPatterns,
			"mode":              skill.Mode,
			"updated_at":        time.Now(),
		},
	}

	_, err = coll.UpdateOne(ctx, bson.M{"_id": objID}, update)
	if err != nil {
		return fmt.Errorf("failed to update skill: %w", err)
	}

	return nil
}

// DeleteSkill deletes a skill (only non-builtin)
func (s *SkillService) DeleteSkill(ctx context.Context, id string) error {
	coll := s.mongoDB.Collection(database.CollectionSkills)

	objID, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return fmt.Errorf("invalid skill ID: %w", err)
	}

	// Only allow deleting non-builtin skills
	result, err := coll.DeleteOne(ctx, bson.M{"_id": objID, "is_builtin": false})
	if err != nil {
		return fmt.Errorf("failed to delete skill: %w", err)
	}
	if result.DeletedCount == 0 {
		return fmt.Errorf("skill not found or is a built-in skill")
	}

	// Also clean up user_skills references
	userSkillsColl := s.mongoDB.Collection(database.CollectionUserSkills)
	_, _ = userSkillsColl.DeleteMany(ctx, bson.M{"skill_id": objID})

	return nil
}

// GetUserSkills returns all skills with the user's enabled status
func (s *SkillService) GetUserSkills(ctx context.Context, userID string) ([]models.UserSkillWithDetails, error) {
	coll := s.mongoDB.Collection(database.CollectionUserSkills)

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"user_id": userID}}},
		{{Key: "$lookup", Value: bson.M{
			"from":         database.CollectionSkills,
			"localField":   "skill_id",
			"foreignField": "_id",
			"as":           "skill_arr",
		}}},
		{{Key: "$unwind", Value: "$skill_arr"}},
		{{Key: "$addFields", Value: bson.M{"skill": "$skill_arr"}}},
		{{Key: "$project", Value: bson.M{"skill_arr": 0}}},
		{{Key: "$sort", Value: bson.D{{Key: "skill.category", Value: 1}, {Key: "skill.name", Value: 1}}}},
	}

	cursor, err := coll.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("failed to get user skills: %w", err)
	}
	defer cursor.Close(ctx)

	var results []models.UserSkillWithDetails
	if err := cursor.All(ctx, &results); err != nil {
		return nil, fmt.Errorf("failed to decode user skills: %w", err)
	}

	return results, nil
}

// EnableSkill enables a skill for a user
func (s *SkillService) EnableSkill(ctx context.Context, userID string, skillID string) error {
	coll := s.mongoDB.Collection(database.CollectionUserSkills)

	objID, err := primitive.ObjectIDFromHex(skillID)
	if err != nil {
		return fmt.Errorf("invalid skill ID: %w", err)
	}

	// Verify skill exists
	skillsColl := s.mongoDB.Collection(database.CollectionSkills)
	count, err := skillsColl.CountDocuments(ctx, bson.M{"_id": objID})
	if err != nil || count == 0 {
		return fmt.Errorf("skill not found")
	}

	filter := bson.M{"user_id": userID, "skill_id": objID}
	update := bson.M{
		"$set": bson.M{
			"enabled": true,
		},
		"$setOnInsert": bson.M{
			"user_id":    userID,
			"skill_id":   objID,
			"created_at": time.Now(),
		},
	}
	opts := options.Update().SetUpsert(true)

	_, err = coll.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		return fmt.Errorf("failed to enable skill: %w", err)
	}

	return nil
}

// DisableSkill disables a skill for a user
func (s *SkillService) DisableSkill(ctx context.Context, userID string, skillID string) error {
	coll := s.mongoDB.Collection(database.CollectionUserSkills)

	objID, err := primitive.ObjectIDFromHex(skillID)
	if err != nil {
		return fmt.Errorf("invalid skill ID: %w", err)
	}

	_, err = coll.UpdateOne(ctx, bson.M{"user_id": userID, "skill_id": objID}, bson.M{"$set": bson.M{"enabled": false}})
	if err != nil {
		return fmt.Errorf("failed to disable skill: %w", err)
	}

	return nil
}

// BulkEnableSkills enables multiple skills for a user at once
func (s *SkillService) BulkEnableSkills(ctx context.Context, userID string, skillIDs []string) error {
	for _, sid := range skillIDs {
		if err := s.EnableSkill(ctx, userID, sid); err != nil {
			log.Printf("⚠️ Failed to enable skill %s for user %s: %v", sid, userID, err)
		}
	}
	return nil
}

// RouteMessage finds the best matching skill for a user message
func (s *SkillService) RouteMessage(ctx context.Context, userID string, message string) (*models.Skill, error) {
	if message == "" {
		return nil, nil
	}

	// Get user's enabled auto-mode skills
	coll := s.mongoDB.Collection(database.CollectionUserSkills)

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"user_id": userID, "enabled": true}}},
		{{Key: "$lookup", Value: bson.M{
			"from":         database.CollectionSkills,
			"localField":   "skill_id",
			"foreignField": "_id",
			"as":           "skill_arr",
		}}},
		{{Key: "$unwind", Value: "$skill_arr"}},
		{{Key: "$match", Value: bson.M{"skill_arr.mode": "auto"}}},
		{{Key: "$replaceRoot", Value: bson.M{"newRoot": "$skill_arr"}}},
	}

	cursor, err := coll.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("failed to get enabled skills: %w", err)
	}
	defer cursor.Close(ctx)

	var enabledSkills []models.Skill
	if err := cursor.All(ctx, &enabledSkills); err != nil {
		return nil, fmt.Errorf("failed to decode skills: %w", err)
	}

	if len(enabledSkills) == 0 {
		return nil, nil
	}

	// Tokenize the message
	messageLower := strings.ToLower(message)
	messageTokens := skillTokenize(messageLower)

	var bestSkill *models.Skill
	bestScore := 0

	for i, skill := range enabledSkills {
		score := 0

		// Check trigger patterns (prefix match) — high confidence signal
		for _, pattern := range skill.TriggerPatterns {
			if strings.HasPrefix(messageLower, strings.ToLower(pattern)) {
				score += 20
			} else if strings.Contains(messageLower, strings.ToLower(pattern)) {
				score += 10
			}
		}

		// Check keyword matches
		for _, token := range messageTokens {
			for _, keyword := range skill.Keywords {
				kwLower := strings.ToLower(keyword)
				if token == kwLower {
					score += 10
					break
				}
				if strings.Contains(kwLower, token) || strings.Contains(token, kwLower) {
					score += 5
					break
				}
			}
		}

		if score > bestScore {
			bestScore = score
			bestSkill = &enabledSkills[i]
		}
	}

	// Threshold: require minimum confidence to activate a skill
	if bestScore >= 15 && bestSkill != nil {
		return bestSkill, nil
	}

	return nil, nil
}

// skillTokenize splits a message into lowercase tokens for matching
func skillTokenize(text string) []string {
	text = strings.ReplaceAll(text, "-", " ")
	text = strings.ReplaceAll(text, "_", " ")
	text = strings.ReplaceAll(text, "/", " ")

	tokens := strings.Fields(text)
	tokenSet := make(map[string]bool)
	unique := []string{}
	for _, token := range tokens {
		token = strings.ToLower(token)
		if !tokenSet[token] && token != "" {
			tokenSet[token] = true
			unique = append(unique, token)
		}
	}
	return unique
}

// ═══════════════════════════════════════════════════════════════════════════
// SKILL.MD IMPORT / EXPORT / COMMUNITY
// ═══════════════════════════════════════════════════════════════════════════

// ImportFromSkillMD parses SKILL.md content and creates a new skill
func (s *SkillService) ImportFromSkillMD(ctx context.Context, content string, authorID string) (*models.Skill, error) {
	fm, body, err := ParseSkillMD(content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SKILL.md: %w", err)
	}

	if body == "" {
		return nil, fmt.Errorf("SKILL.md has no instruction body")
	}

	skill := SkillMDToSkill(fm, body)

	// Override author with the importing user
	if authorID != "" {
		skill.AuthorID = authorID
	}

	// If name is still empty after parsing, generate one
	if skill.Name == "" {
		skill.Name = "Imported Skill"
	}

	// Check if a non-builtin skill with this name already exists — if so, return it
	coll := s.mongoDB.Collection(database.CollectionSkills)
	var existing models.Skill
	err = coll.FindOne(ctx, bson.M{"name": skill.Name, "is_builtin": false}).Decode(&existing)
	if err == nil {
		// Skill already imported — return the existing one
		return &existing, nil
	}

	if err := s.CreateSkill(ctx, skill); err != nil {
		// Handle duplicate key race condition
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "E11000") {
			// Try to find and return the existing one
			err2 := coll.FindOne(ctx, bson.M{"name": skill.Name, "is_builtin": false}).Decode(&existing)
			if err2 == nil {
				return &existing, nil
			}
		}
		return nil, fmt.Errorf("failed to create imported skill: %w", err)
	}

	return skill, nil
}

// ImportFromGitHubURL fetches a SKILL.md from a GitHub URL and imports it
func (s *SkillService) ImportFromGitHubURL(ctx context.Context, githubURL string, authorID string) (*models.Skill, error) {
	rawURL, err := resolveGitHubRawURL(githubURL)
	if err != nil {
		return nil, fmt.Errorf("invalid GitHub URL: %w", err)
	}

	// Fetch the SKILL.md content
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch SKILL.md: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned status %d for %s", resp.StatusCode, rawURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSkillMDSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read SKILL.md: %w", err)
	}

	return s.ImportFromSkillMD(ctx, string(body), authorID)
}

// ExportAsSkillMD converts an existing skill to SKILL.md format
func (s *SkillService) ExportAsSkillMD(ctx context.Context, skillID string) (string, error) {
	skill, err := s.GetSkill(ctx, skillID)
	if err != nil {
		return "", fmt.Errorf("skill not found: %w", err)
	}

	return SkillToSkillMD(skill), nil
}

// FetchCommunitySkills fetches the skill catalog from the anthropics/skills GitHub repo (cached)
func (s *SkillService) FetchCommunitySkills(ctx context.Context) ([]CommunitySkillEntry, error) {
	const cacheKey = "community_skills"

	// Check cache
	if cached, found := s.communityCache.Get(cacheKey); found {
		if entries, ok := cached.([]CommunitySkillEntry); ok {
			return entries, nil
		}
	}

	// Fetch directory listing from GitHub Contents API
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/anthropics/skills/contents/", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "ClaraVerse/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch GitHub directory: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("GitHub API rate limit exceeded, try again later")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var items []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, fmt.Errorf("failed to decode GitHub response: %w", err)
	}

	// Filter to directories only (each directory is a skill)
	var dirs []string
	for _, item := range items {
		if item.Type == "dir" && !strings.HasPrefix(item.Name, ".") {
			dirs = append(dirs, item.Name)
		}
	}

	// Fetch SKILL.md for each directory in parallel (max 5 concurrent)
	type result struct {
		entry CommunitySkillEntry
		ok    bool
	}
	results := make([]result, len(dirs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)

	for i, dir := range dirs {
		wg.Add(1)
		go func(idx int, dirName string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			rawURL := fmt.Sprintf("https://raw.githubusercontent.com/anthropics/skills/main/%s/SKILL.md", dirName)
			resp, err := client.Get(rawURL)
			if err != nil || resp.StatusCode != http.StatusOK {
				if resp != nil {
					resp.Body.Close()
				}
				return
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(io.LimitReader(resp.Body, maxSkillMDSize))
			if err != nil {
				return
			}

			fm, _, parseErr := ParseSkillMD(string(body))
			if parseErr != nil {
				return
			}

			name := fm.Name
			if name == "" {
				name = dirName
			}
			displayName := kebabToTitleCase(name)
			description := fm.Description
			category := inferCategory(name, description)

			author := ""
			if fm.Metadata != nil {
				author = fm.Metadata["author"]
			}
			if author == "" {
				author = "anthropic"
			}

			results[idx] = result{
				entry: CommunitySkillEntry{
					Name:        displayName,
					Description: description,
					RepoURL:     fmt.Sprintf("https://github.com/anthropics/skills/tree/main/%s", dirName),
					RawURL:      rawURL,
					Author:      author,
					License:     fm.License,
					Category:    category,
					Icon:        categoryDefaultIcon(category),
				},
				ok: true,
			}
		}(i, dir)
	}
	wg.Wait()

	// Collect successful results
	entries := make([]CommunitySkillEntry, 0, len(dirs))
	for _, r := range results {
		if r.ok {
			entries = append(entries, r.entry)
		}
	}

	// Cache the results
	s.communityCache.Set(cacheKey, entries, cache.DefaultExpiration)
	log.Printf("🌐 [SKILLS] Fetched %d community skills from anthropics/skills", len(entries))

	return entries, nil
}

// resolveGitHubRawURL converts various GitHub URL formats to the raw SKILL.md URL.
// Supports:
//   - https://github.com/{owner}/{repo}/tree/{branch}/{path}
//   - https://github.com/{owner}/{repo}/blob/{branch}/{path}/SKILL.md
//   - https://raw.githubusercontent.com/{owner}/{repo}/{branch}/{path}/SKILL.md
func resolveGitHubRawURL(githubURL string) (string, error) {
	parsed, err := url.Parse(githubURL)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	// Already a raw URL
	if parsed.Host == "raw.githubusercontent.com" {
		if strings.HasSuffix(parsed.Path, "/SKILL.md") || strings.HasSuffix(parsed.Path, "/skill.md") {
			return githubURL, nil
		}
		return githubURL + "/SKILL.md", nil
	}

	if parsed.Host != "github.com" {
		return "", fmt.Errorf("not a GitHub URL")
	}

	// Parse path: /{owner}/{repo}/{tree|blob}/{branch}/{...path}
	parts := strings.SplitN(strings.TrimPrefix(parsed.Path, "/"), "/", 5)
	if len(parts) < 2 {
		return "", fmt.Errorf("could not parse GitHub path: need at least owner/repo")
	}

	// Handle short URLs like github.com/owner/repo (no tree/blob)
	if len(parts) < 4 || (parts[2] != "tree" && parts[2] != "blob") {
		owner := parts[0]
		repo := parts[1]
		// Try main branch, append remaining path if any
		extraPath := ""
		if len(parts) > 2 {
			extraPath = strings.Join(parts[2:], "/")
		}
		if extraPath != "" {
			if strings.HasSuffix(extraPath, "SKILL.md") || strings.HasSuffix(extraPath, "skill.md") {
				return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/main/%s", owner, repo, extraPath), nil
			}
			return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/main/%s/SKILL.md", owner, repo, extraPath), nil
		}
		return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/main/SKILL.md", owner, repo), nil
	}

	owner := parts[0]
	repo := parts[1]
	// parts[2] is "tree" or "blob"
	branch := parts[3]
	skillPath := ""
	if len(parts) > 4 {
		skillPath = parts[4]
	}

	// If path already points to SKILL.md, use it directly
	if strings.HasSuffix(skillPath, "SKILL.md") || strings.HasSuffix(skillPath, "skill.md") {
		return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", owner, repo, branch, skillPath), nil
	}

	// Otherwise, assume it's a directory and append /SKILL.md
	if skillPath != "" {
		return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s/SKILL.md", owner, repo, branch, skillPath), nil
	}

	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/SKILL.md", owner, repo, branch), nil
}
