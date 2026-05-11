package model

import (
	"time"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// SessionTemplate
// ---------------------------------------------------------------------------

// SessionTemplate stores a reusable session configuration so the user can
// quickly launch common agent + repo + command combinations.
type SessionTemplate struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	AgentType string   `json:"agentType"`
	RepoPaths []string `json:"repoPaths"` // stored as JSON in DB
	Command   string   `json:"command"`
	CreatedAt time.Time `json:"createdAt"`
}

// NewSessionTemplate constructs a SessionTemplate with a generated UUID and
// CreatedAt set to the current time.
func NewSessionTemplate(name, agentType string, repoPaths []string, command string) SessionTemplate {
	return SessionTemplate{
		ID:        uuid.New().String(),
		Name:      name,
		AgentType: agentType,
		RepoPaths: repoPaths,
		Command:   command,
		CreatedAt: time.Now(),
	}
}

// ---------------------------------------------------------------------------
// TemplateParam
// ---------------------------------------------------------------------------

// TemplateParam defines a user-configurable parameter for a session template
// (recipe). Parameters can be substituted into prompt templates at launch time.
type TemplateParam struct {
	ID           string `json:"id"`
	TemplateID   string `json:"templateId"`
	Name         string `json:"name"`
	ParamType    string `json:"paramType"` // "string", "boolean", "select"
	DefaultValue string `json:"defaultValue"`
	Description  string `json:"description"`
	SortOrder    int    `json:"sortOrder"`
}

// NewTemplateParam constructs a TemplateParam with a generated UUID.
func NewTemplateParam(templateID, name, paramType, defaultValue, description string, sortOrder int) TemplateParam {
	return TemplateParam{
		ID:           uuid.New().String(),
		TemplateID:   templateID,
		Name:         name,
		ParamType:    paramType,
		DefaultValue: defaultValue,
		Description:  description,
		SortOrder:    sortOrder,
	}
}

// ---------------------------------------------------------------------------
// RecipeStep
// ---------------------------------------------------------------------------

// RecipeStep defines a single step within a multi-step recipe (template). Each
// step specifies an agent type and a prompt template that may contain {param}
// placeholders for TemplateParam values. Steps can declare dependencies on
// other steps via the DependsOn field.
type RecipeStep struct {
	ID             string `json:"id"`
	TemplateID     string `json:"templateId"`
	AgentType      string `json:"agentType"`
	PromptTemplate string `json:"promptTemplate"` // may contain {param} placeholders
	DependsOn      string `json:"dependsOn"`      // comma-separated step IDs
	SortOrder      int    `json:"sortOrder"`
}

// NewRecipeStep constructs a RecipeStep with a generated UUID.
func NewRecipeStep(templateID, agentType, promptTemplate, dependsOn string, sortOrder int) RecipeStep {
	return RecipeStep{
		ID:             uuid.New().String(),
		TemplateID:     templateID,
		AgentType:      agentType,
		PromptTemplate: promptTemplate,
		DependsOn:      dependsOn,
		SortOrder:      sortOrder,
	}
}
