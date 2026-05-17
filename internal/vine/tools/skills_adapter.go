package tools

import (
	"context"

	"github.com/aspectrr/ivy/internal/vine/skills"
)

// SkillsStoreAdapter adapts skills.Store to implement SkillSearcher and SkillCreator.
type SkillsStoreAdapter struct {
	Store *skills.Store
}

// SearchByText implements SkillSearcher.
func (a *SkillsStoreAdapter) SearchByText(ctx context.Context, query string, limit int) ([]SkillSearchResult, error) {
	results, err := a.Store.SearchByText(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]SkillSearchResult, len(results))
	for i, r := range results {
		out[i] = SkillSearchResult{
			ID:          r.ID,
			Name:        r.Name,
			Description: r.Description,
			Content:     r.Content,
			BuiltIn:     r.BuiltIn,
		}
	}
	return out, nil
}

// Create implements SkillCreator.
func (a *SkillsStoreAdapter) Create(ctx context.Context, name, description, content string, sourceSessionID *string) (*SkillSearchResult, error) {
	skill, err := a.Store.Create(ctx, name, description, content, sourceSessionID)
	if err != nil {
		return nil, err
	}
	return &SkillSearchResult{
		ID:          skill.ID,
		Name:        skill.Name,
		Description: skill.Description,
		Content:     skill.Content,
		BuiltIn:     skill.BuiltIn,
	}, nil
}
