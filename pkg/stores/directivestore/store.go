// Package directivestore provides a MongoDB-backed store for directives,
// initiatives, and teams. It is the public-facing API that wraps the internal
// teams store and resolves collections from otoxan_global (teams) and per-team
// databases (directives, initiatives).
package directivestore

import (
	"context"
	"fmt"

	"github.com/silas/otoxan/internal/state"
	"github.com/silas/otoxan/internal/store/teams"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ------------------------------------------------------------------
// Re-exported types (public API surface)
// ------------------------------------------------------------------

// TeamListOptions configures the List query for teams.
type TeamListOptions = teams.TeamListOptions

// DirectiveListOptions configures the List query for directives.
type DirectiveListOptions = teams.DirectiveListOptions

// InitiativeListOptions configures the List query for initiatives.
type InitiativeListOptions = teams.InitiativeListOptions

// DirectiveStatus enumerates the possible states of a team directive.
type DirectiveStatus = teams.DirectiveStatus

// InitiativeStatus enumerates the possible states of an initiative.
type InitiativeStatus = teams.InitiativeStatus

// TeamStatus enumerates the possible states of a team.
type TeamStatus = teams.TeamStatus

// AgentStatus enumerates the possible states of an agent.
type AgentStatus = teams.AgentStatus

const (
	DirectiveActive  = teams.DirectiveActive
	DirectiveRevised = teams.DirectiveRevised
	DirectiveRetired = teams.DirectiveRetired
)

const (
	InitiativeProposed  = teams.InitiativeProposed
	InitiativeActive    = teams.InitiativeActive
	InitiativeMeasuring = teams.InitiativeMeasuring
	InitiativeSucceeded = teams.InitiativeSucceeded
	InitiativeFailed    = teams.InitiativeFailed
	InitiativePivoted   = teams.InitiativePivoted
)

const (
	StatusForming = teams.StatusForming
	StatusActive  = teams.StatusActive
	StatusPaused  = teams.StatusPaused
	StatusRetired = teams.StatusRetired
)

const (
	AgentActive   = teams.AgentActive
	AgentInactive = teams.AgentInactive
	AgentRetired  = teams.AgentRetired
)

// Member represents a team member.
type Member = teams.Member

// SuccessCriterion represents a measurable target for a directive or initiative.
type SuccessCriterion = teams.SuccessCriterion

// Timeline captures initiative lifecycle timestamps.
type Timeline = teams.Timeline

// Team is the canonical BSON shape for a team document.
type Team = teams.Team

// Agent is the canonical BSON shape for an agent document.
type Agent = teams.Agent

// Directive is the canonical BSON shape for a team directive.
type Directive = teams.Directive

// Initiative is the canonical BSON shape for a team initiative.
type Initiative = teams.Initiative

// ------------------------------------------------------------------
// Store
// ------------------------------------------------------------------

// Store is the public-facing MongoDB-backed store for directives, initiatives,
// and teams. It resolves collections from otoxan_global and per-team DBs.
type Store struct {
	client          *mongo.Client
	teamStore       *teams.TeamStore
	directiveStore  *teams.DirectiveStore
	initiativeStore *teams.InitiativeStore
	teamID          string
}

// NewStore creates a Store backed by the given MongoDB client. If teamID is
// non-empty, it resolves the per-team DB for directives and initiatives;
// otherwise directives/initiatives are accessed from otoxan_global.
func NewStore(client *mongo.Client, teamID string) (*Store, error) {
	globalDB := state.GlobalDB(client)

	// Teams always live in otoxan_global.teams
	ts := teams.NewTeamStore(globalDB.Collection("teams"))

	var ds *teams.DirectiveStore
	var is *teams.InitiativeStore

	if teamID != "" {
		// Per-team DB: team_{team_id}.directives / .initiatives
		teamDB := client.Database(fmt.Sprintf("team_%s", teamID))
		ds = teams.NewDirectiveStore(teamDB.Collection("directives"))
		is = teams.NewInitiativeStore(teamDB.Collection("initiatives"))
	} else {
		// Global fallback
		ds = teams.NewDirectiveStore(globalDB.Collection("directives"))
		is = teams.NewInitiativeStore(globalDB.Collection("initiatives"))
	}

	return &Store{
		client:          client,
		teamStore:       ts,
		directiveStore:  ds,
		initiativeStore: is,
		teamID:          teamID,
	}, nil
}

// ------------------------------------------------------------------
// Team CRUD
// ------------------------------------------------------------------

// CreateTeam inserts a new team document. The caller must set TeamID and Name.
func (s *Store) CreateTeam(ctx context.Context, team *Team) (*mongo.InsertOneResult, error) {
	return s.teamStore.Create(ctx, team)
}

// GetTeam retrieves a single team by team_id.
func (s *Store) GetTeam(ctx context.Context, teamID string) (*Team, error) {
	return s.teamStore.Get(ctx, teamID)
}

// GetTeamWithDeleted retrieves a team including soft-deleted ones.
func (s *Store) GetTeamWithDeleted(ctx context.Context, teamID string) (*Team, error) {
	return s.teamStore.GetWithDeleted(ctx, teamID)
}

// UpdateTeam patches fields of an existing team.
func (s *Store) UpdateTeam(ctx context.Context, teamID string, updates bson.M) (*mongo.UpdateResult, error) {
	return s.teamStore.Update(ctx, teamID, updates)
}

// DeleteTeam soft-deletes a team by team_id.
func (s *Store) DeleteTeam(ctx context.Context, teamID string) (*mongo.UpdateResult, error) {
	return s.teamStore.Delete(ctx, teamID)
}

// RestoreTeam un-deletes a soft-deleted team.
func (s *Store) RestoreTeam(ctx context.Context, teamID string) (*mongo.UpdateResult, error) {
	return s.teamStore.Restore(ctx, teamID)
}

// HardDeleteTeam permanently removes a team.
func (s *Store) HardDeleteTeam(ctx context.Context, teamID string) (*mongo.DeleteResult, error) {
	return s.teamStore.HardDelete(ctx, teamID)
}

// ListTeams returns teams matching the provided filters.
func (s *Store) ListTeams(ctx context.Context, opts teams.TeamListOptions) ([]Team, error) {
	return s.teamStore.List(ctx, opts)
}

// CountTeams returns the number of teams matching the filter.
func (s *Store) CountTeams(ctx context.Context, filter bson.M) (int64, error) {
	return s.teamStore.Count(ctx, filter)
}

// AddMember pushes a member into the team's members array.
func (s *Store) AddMember(ctx context.Context, teamID string, member Member) (*mongo.UpdateResult, error) {
	return s.teamStore.AddMember(ctx, teamID, member)
}

// RemoveMember pulls a member by agent name from the team's members array.
func (s *Store) RemoveMember(ctx context.Context, teamID string, agent string) (*mongo.UpdateResult, error) {
	return s.teamStore.RemoveMember(ctx, teamID, agent)
}

// ------------------------------------------------------------------
// Directive CRUD
// ------------------------------------------------------------------

// CreateDirective inserts a new team directive document.
func (s *Store) CreateDirective(ctx context.Context, d *Directive) (*mongo.InsertOneResult, error) {
	return s.directiveStore.Create(ctx, d)
}

// GetDirective retrieves a single directive by directive_id.
func (s *Store) GetDirective(ctx context.Context, directiveID string) (*Directive, error) {
	return s.directiveStore.Get(ctx, directiveID)
}

// GetDirectiveWithDeleted retrieves a directive including soft-deleted ones.
func (s *Store) GetDirectiveWithDeleted(ctx context.Context, directiveID string) (*Directive, error) {
	return s.directiveStore.GetWithDeleted(ctx, directiveID)
}

// UpdateDirective patches fields of an existing directive.
func (s *Store) UpdateDirective(ctx context.Context, directiveID string, updates bson.M) (*mongo.UpdateResult, error) {
	return s.directiveStore.Update(ctx, directiveID, updates)
}

// DeleteDirective soft-deletes a directive by directive_id.
func (s *Store) DeleteDirective(ctx context.Context, directiveID string) (*mongo.UpdateResult, error) {
	return s.directiveStore.Delete(ctx, directiveID)
}

// RestoreDirective un-deletes a soft-deleted directive.
func (s *Store) RestoreDirective(ctx context.Context, directiveID string) (*mongo.UpdateResult, error) {
	return s.directiveStore.Restore(ctx, directiveID)
}

// HardDeleteDirective permanently removes a directive.
func (s *Store) HardDeleteDirective(ctx context.Context, directiveID string) (*mongo.DeleteResult, error) {
	return s.directiveStore.HardDelete(ctx, directiveID)
}

// ListDirectives returns directives matching the provided filters.
func (s *Store) ListDirectives(ctx context.Context, opts teams.DirectiveListOptions) ([]Directive, error) {
	return s.directiveStore.List(ctx, opts)
}

// CountDirectives returns the number of directives matching the filter.
func (s *Store) CountDirectives(ctx context.Context, filter bson.M) (int64, error) {
	return s.directiveStore.Count(ctx, filter)
}

// ------------------------------------------------------------------
// Initiative CRUD
// ------------------------------------------------------------------

// CreateInitiative inserts a new initiative document.
func (s *Store) CreateInitiative(ctx context.Context, in *Initiative) (*mongo.InsertOneResult, error) {
	return s.initiativeStore.Create(ctx, in)
}

// GetInitiative retrieves a single initiative by initiative_id.
func (s *Store) GetInitiative(ctx context.Context, initiativeID string) (*Initiative, error) {
	return s.initiativeStore.Get(ctx, initiativeID)
}

// GetInitiativeWithDeleted retrieves an initiative including soft-deleted ones.
func (s *Store) GetInitiativeWithDeleted(ctx context.Context, initiativeID string) (*Initiative, error) {
	return s.initiativeStore.GetWithDeleted(ctx, initiativeID)
}

// UpdateInitiative patches fields of an existing initiative.
func (s *Store) UpdateInitiative(ctx context.Context, initiativeID string, updates bson.M) (*mongo.UpdateResult, error) {
	return s.initiativeStore.Update(ctx, initiativeID, updates)
}

// DeleteInitiative soft-deletes an initiative by initiative_id.
func (s *Store) DeleteInitiative(ctx context.Context, initiativeID string) (*mongo.UpdateResult, error) {
	return s.initiativeStore.Delete(ctx, initiativeID)
}

// RestoreInitiative un-deletes a soft-deleted initiative.
func (s *Store) RestoreInitiative(ctx context.Context, initiativeID string) (*mongo.UpdateResult, error) {
	return s.initiativeStore.Restore(ctx, initiativeID)
}

// HardDeleteInitiative permanently removes an initiative.
func (s *Store) HardDeleteInitiative(ctx context.Context, initiativeID string) (*mongo.DeleteResult, error) {
	return s.initiativeStore.HardDelete(ctx, initiativeID)
}

// ListInitiatives returns initiatives matching the provided filters.
func (s *Store) ListInitiatives(ctx context.Context, opts teams.InitiativeListOptions) ([]Initiative, error) {
	return s.initiativeStore.List(ctx, opts)
}

// CountInitiatives returns the number of initiatives matching the filter.
func (s *Store) CountInitiatives(ctx context.Context, filter bson.M) (int64, error) {
	return s.initiativeStore.Count(ctx, filter)
}

// ------------------------------------------------------------------
// AddPlanToInitiative — bidirectional linkage
// ------------------------------------------------------------------

// AddPlanToInitiative links a plan to an initiative bidirectionally.
// It pushes the plan_id into the initiative's plan_ids array and also
// updates the plan document to set its initiative_id field.
// This mirrors teamstore.py:add_plan.
func (s *Store) AddPlanToInitiative(ctx context.Context, initiativeID string, planID string) (*mongo.UpdateResult, error) {
	// Resolve the plan collection. Plans live in the default agent DB.
	// For the bidirectional update we use the global DB as a fallback
	// or the caller's agent DB. Since we don't have an agent context here,
	// we look in otoxan_global.plans first, then fall back to a generic
	// plans collection if the caller has set one.
	planColl := state.GlobalDB(s.client).Collection("plans")
	return s.initiativeStore.AddPlanToInitiative(ctx, initiativeID, planID, planColl)
}

// AddPlanToInitiativeWithColl links a plan to an initiative bidirectionally
// using an explicit plan collection. Use this when plans live in a
// per-agent database rather than the global DB.
func (s *Store) AddPlanToInitiativeWithColl(ctx context.Context, initiativeID string, planID string, planColl *mongo.Collection) (*mongo.UpdateResult, error) {
	return s.initiativeStore.AddPlanToInitiative(ctx, initiativeID, planID, planColl)
}

// RemovePlanFromInitiative removes a plan_id from an initiative's plan_ids array.
func (s *Store) RemovePlanFromInitiative(ctx context.Context, initiativeID string, planID string) (*mongo.UpdateResult, error) {
	return s.initiativeStore.RemovePlanFromInitiative(ctx, initiativeID, planID)
}
