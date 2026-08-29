package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/identity"
	"github.com/agentstation/starport/internal/server/dto"
)

// Member listing bounds. They match the account listing so an operator paging
// two admin collections meets one contract.
const (
	memberListDefaultLimit = 100
	memberListMaxLimit     = 1000
)

const (
	fieldUserID = "user_id"
	fieldTeamID = "team_id"
)

// MembersController serves the deployment's people plane: the users an
// identity provider resolved, the teams an operator forms from them, and the
// account grants that give an account to one user or one team. The accounts
// themselves live on the accounts surface; a grant only names one, the way a
// shared credential's grant list names accounts without owning them.
type MembersController struct {
	identity identity.Repositories
}

// NewMembersController creates the members controller. Zero repositories —
// a deployment with no identity configured — degrade every route to 503
// rather than to an empty list, which would read as "nobody is here" on a
// gateway that never looked.
func NewMembersController(repositories identity.Repositories) *MembersController {
	return &MembersController{identity: repositories}
}

// ready reports whether identity storage is configured, writing the refusal
// itself when it is not.
func (h *MembersController) ready(w http.ResponseWriter) bool {
	if h == nil || h.identity.Users == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError,
			"Identity is not configured for this gateway")
		return false
	}
	return true
}

// ListUsers handles GET /api/v1/admin/users.
func (h *MembersController) ListUsers(w http.ResponseWriter, r *http.Request) {
	listPeople(h, w, r, "users",
		func(ctx context.Context, limit, offset int) ([]identity.UserRecord, error) {
			return h.identity.Users.List(ctx, limit, offset)
		},
		func(record identity.UserRecord) identity.User { return record.User })
}

// ListTeams handles GET /api/v1/admin/teams.
func (h *MembersController) ListTeams(w http.ResponseWriter, r *http.Request) {
	listPeople(h, w, r, "teams",
		func(ctx context.Context, limit, offset int) ([]identity.TeamRecord, error) {
			return h.identity.Teams.List(ctx, limit, offset)
		},
		func(record identity.TeamRecord) identity.Team { return record.Team })
}

// listPeople serves one paged listing over either people collection: the
// ready guard, the shared window, the limit+1 probe that proves or disproves
// a following page, and the envelope keyed by the collection's plural. The
// repository call arrives as a closure so a nil repository is never touched
// before the guard refuses.
func listPeople[R, V any](h *MembersController, w http.ResponseWriter, r *http.Request,
	plural string,
	list func(ctx context.Context, limit, offset int) ([]R, error),
	value func(R) V,
) {
	if !h.ready(w) {
		return
	}

	limit, offset, ok := listWindow(w, r)
	if !ok {
		return
	}

	records, err := list(r.Context(), limit+1, offset)
	if err != nil {
		message := "Failed to list " + plural
		log.Error().Err(err).Msg(message)
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, message)
		return
	}
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}

	values := make([]V, 0, len(records))
	for _, record := range records {
		values = append(values, value(record))
	}
	writeMembersJSON(w, http.StatusOK, map[string]any{
		plural:                  values,
		responseCountField:      len(values),
		responsePaginationField: paginationEnvelope(limit, offset, hasMore),
	})
}

// CreateTeam handles POST /api/v1/admin/teams. The gateway names the team
// itself: a team ID is a join key for memberships and grants, not a word an
// operator should have to invent.
func (h *MembersController) CreateTeam(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	var request struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}

	candidate := identity.Team{ID: uuid.NewString(), Name: request.Name}
	if err := candidate.Validate(); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}

	record, err := h.identity.Teams.Create(r.Context(), candidate)
	if err != nil {
		log.Error().Err(err).Str(fieldTeamID, candidate.ID).Msg("Failed to create team")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to create team")
		return
	}
	writeMembersJSON(w, http.StatusCreated, record.Team)
}

// DeleteTeam handles DELETE /api/v1/admin/teams/{team_id}. Deleting a team
// takes its memberships and its account grants with it: both rows are access
// control, so neither may outlive the team they name.
func (h *MembersController) DeleteTeam(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	record, ok := h.readTeam(w, r)
	if !ok {
		return
	}

	if err := h.identity.Teams.Delete(r.Context(), record.Team.ID, record.Revision); err != nil {
		switch {
		case errors.Is(err, identity.ErrTeamNotFound):
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Team not found")
		case errors.Is(err, identity.ErrTeamConflict):
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
				"Team changed during delete")
		default:
			log.Error().Err(err).Str(fieldTeamID, record.Team.ID).Msg("Failed to delete team")
			dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to delete team")
		}
		return
	}
	writeMembersJSON(w, http.StatusOK, map[string]any{
		responseMessageField: "Team deleted successfully",
		fieldTeamID:          record.Team.ID,
	})
}

// ListTeamMembers handles GET /api/v1/admin/teams/{team_id}/members. It reads
// the team first so an unknown team answers 404 rather than an empty roster.
func (h *MembersController) ListTeamMembers(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	record, ok := h.readTeam(w, r)
	if !ok {
		return
	}

	members, err := h.identity.Memberships.ListByTeam(r.Context(), record.Team.ID)
	if err != nil {
		log.Error().Err(err).Str(fieldTeamID, record.Team.ID).Msg("Failed to list team members")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to list team members")
		return
	}
	writeMembersJSON(w, http.StatusOK, map[string]any{
		"members":          members,
		responseCountField: len(members),
	})
}

// AddTeamMember handles PUT /api/v1/admin/teams/{team_id}/members/{user_id}.
// PUT because the request states a fact — this user is on this team — and the
// whole fact is in the path.
func (h *MembersController) AddTeamMember(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	membership := identity.Membership{
		UserID: chi.URLParam(r, fieldUserID),
		TeamID: chi.URLParam(r, fieldTeamID),
	}
	created, err := h.identity.Memberships.Add(r.Context(), membership)
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrMissingID):
			dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		case errors.Is(err, identity.ErrUserNotFound):
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "User not found")
		case errors.Is(err, identity.ErrTeamNotFound):
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Team not found")
		case errors.Is(err, identity.ErrMembershipConflict):
			dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
				"This user is already on this team")
		default:
			log.Error().Err(err).Str(fieldTeamID, membership.TeamID).Str(fieldUserID, membership.UserID).
				Msg("Failed to add team member")
			dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to add team member")
		}
		return
	}
	writeMembersJSON(w, http.StatusCreated, created)
}

// RemoveTeamMember handles DELETE /api/v1/admin/teams/{team_id}/members/{user_id}.
func (h *MembersController) RemoveTeamMember(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	userID := chi.URLParam(r, fieldUserID)
	teamID := chi.URLParam(r, fieldTeamID)
	if err := h.identity.Memberships.Remove(r.Context(), userID, teamID); err != nil {
		if errors.Is(err, identity.ErrMembershipNotFound) {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Membership not found")
			return
		}
		log.Error().Err(err).Str(fieldTeamID, teamID).Str(fieldUserID, userID).
			Msg("Failed to remove team member")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to remove team member")
		return
	}
	writeMembersJSON(w, http.StatusOK, map[string]any{
		responseMessageField: "Membership removed successfully",
		fieldUserID:          userID,
		fieldTeamID:          teamID,
	})
}

// ListUserGrants handles GET /api/v1/admin/users/{user_id}/grants: the grants
// that name this user directly, without the ones that reach it through teams.
func (h *MembersController) ListUserGrants(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	record, ok := h.readUser(w, r)
	if !ok {
		return
	}

	grants, err := h.identity.AccountGrants.ListByUser(r.Context(), record.User.ID)
	if err != nil {
		log.Error().Err(err).Str(fieldUserID, record.User.ID).Msg("Failed to list user grants")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to list account grants")
		return
	}
	writeGrants(w, grants)
}

// ListTeamGrants handles GET /api/v1/admin/teams/{team_id}/grants.
func (h *MembersController) ListTeamGrants(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	record, ok := h.readTeam(w, r)
	if !ok {
		return
	}

	grants, err := h.identity.AccountGrants.ListByTeam(r.Context(), record.Team.ID)
	if err != nil {
		log.Error().Err(err).Str(fieldTeamID, record.Team.ID).Msg("Failed to list team grants")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to list account grants")
		return
	}
	writeGrants(w, grants)
}

// ReachableAccounts handles GET /api/v1/admin/users/{user_id}/accounts: every
// account this user's grants reach, the direct ones and the ones that arrive
// through any team the user is on, deduplicated. This is the operator's view
// of the same answer the session gate resolves for the user's own sessions.
func (h *MembersController) ReachableAccounts(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	record, ok := h.readUser(w, r)
	if !ok {
		return
	}

	accounts, err := h.identity.AccountGrants.ReachableAccounts(r.Context(), record.User.ID)
	if err != nil {
		log.Error().Err(err).Str(fieldUserID, record.User.ID).Msg("Failed to resolve reachable accounts")
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, "Failed to resolve reachable accounts")
		return
	}
	writeMembersJSON(w, http.StatusOK, map[string]any{
		"accounts":         accounts,
		responseCountField: len(accounts),
	})
}

// CreateGrant handles POST /api/v1/admin/account-grants. The body names one
// account and exactly one grantee, a user or a team.
func (h *MembersController) CreateGrant(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	var grant identity.AccountGrant
	if err := json.NewDecoder(r.Body).Decode(&grant); err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Invalid request body")
		return
	}

	created, err := h.identity.AccountGrants.Add(r.Context(), grant)
	if err != nil {
		h.writeGrantRefusal(w, grant, err, "Failed to create account grant")
		return
	}
	writeMembersJSON(w, http.StatusCreated, created)
}

// DeleteGrant handles DELETE /api/v1/admin/account-grants. The grant's three
// naming fields arrive as query parameters, because the composite of all
// three is the grant's only identity.
func (h *MembersController) DeleteGrant(w http.ResponseWriter, r *http.Request) {
	if !h.ready(w) {
		return
	}

	grant := identity.AccountGrant{
		AccountID: r.URL.Query().Get(fieldAccountID),
		UserID:    r.URL.Query().Get(fieldUserID),
		TeamID:    r.URL.Query().Get(fieldTeamID),
	}
	if err := h.identity.AccountGrants.Remove(r.Context(), grant); err != nil {
		h.writeGrantRefusal(w, grant, err, "Failed to remove account grant")
		return
	}
	writeMembersJSON(w, http.StatusOK, map[string]any{
		responseMessageField: "Account grant removed successfully",
		fieldAccountID:       grant.AccountID,
	})
}

// writeGrantRefusal maps a grant repository error onto the admin surface. Both
// the create and the delete share it because they share the failure modes: a
// malformed grant, a grantee nobody knows, and the row being or not being there.
func (h *MembersController) writeGrantRefusal(w http.ResponseWriter, grant identity.AccountGrant, err error, message string) {
	switch {
	case errors.Is(err, identity.ErrMissingID), errors.Is(err, identity.ErrGranteeRequired):
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
	case errors.Is(err, identity.ErrUserNotFound):
		dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "User not found")
	case errors.Is(err, identity.ErrTeamNotFound):
		dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Team not found")
	case errors.Is(err, identity.ErrAccountGrantConflict):
		dto.WriteError(w, http.StatusConflict, dto.ErrorTypeInvalidRequest,
			"This account grant already exists")
	case errors.Is(err, identity.ErrAccountGrantNotFound):
		dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, "Account grant not found")
	default:
		log.Error().Err(err).Str(fieldAccountID, grant.AccountID).Msg(message)
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, message)
	}
}

// readUser loads the addressed user, writing the refusal itself when it cannot.
func (h *MembersController) readUser(w http.ResponseWriter, r *http.Request) (identity.UserRecord, bool) {
	return readSubject(w, r, fieldUserID, "user", identity.ErrUserNotFound,
		func(ctx context.Context, id string) (identity.UserRecord, error) {
			return h.identity.Users.GetByID(ctx, id)
		})
}

// readTeam loads the addressed team, writing the refusal itself when it cannot.
func (h *MembersController) readTeam(w http.ResponseWriter, r *http.Request) (identity.TeamRecord, bool) {
	return readSubject(w, r, fieldTeamID, "team", identity.ErrTeamNotFound,
		func(ctx context.Context, id string) (identity.TeamRecord, error) {
			return h.identity.Teams.GetByID(ctx, id)
		})
}

// readSubject loads one addressed row of the people plane, writing the
// refusal itself when it cannot: a missing path parameter is 400, the
// collection's not-found sentinel is 404, and anything else is 500. The
// operator asked about someone specific, so the 404 is honest where an
// empty answer would hide a typo.
func readSubject[R any](w http.ResponseWriter, r *http.Request,
	param, noun string, notFound error,
	get func(ctx context.Context, id string) (R, error),
) (R, bool) {
	var zero R
	id := chi.URLParam(r, param)
	if id == "" {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "Missing "+noun+" id")
		return zero, false
	}
	record, err := get(r.Context(), id)
	if err != nil {
		title := strings.ToUpper(noun[:1]) + noun[1:]
		if errors.Is(err, notFound) {
			dto.WriteError(w, http.StatusNotFound, dto.ErrorTypeNotFound, title+" not found")
			return zero, false
		}
		message := "Failed to read " + noun
		log.Error().Err(err).Str(param, id).Msg(message)
		dto.WriteError(w, http.StatusInternalServerError, dto.ErrorTypeServerError, message)
		return zero, false
	}
	return record, true
}

// listWindow reads the shared limit and offset parameters, writing the
// refusal itself when either is malformed.
func listWindow(w http.ResponseWriter, r *http.Request) (limit, offset int, ok bool) {
	limit, err := positiveQueryInt(r, fieldLimit, memberListDefaultLimit, memberListMaxLimit)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return 0, 0, false
	}
	offset, err = positiveQueryInt(r, "offset", 0, math.MaxInt)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return 0, 0, false
	}
	return limit, offset, true
}

func writeGrants(w http.ResponseWriter, grants []identity.AccountGrant) {
	writeMembersJSON(w, http.StatusOK, map[string]any{
		"grants":           grants,
		responseCountField: len(grants),
	})
}

func writeMembersJSON(w http.ResponseWriter, status int, body any) {
	if err := dto.WriteJSON(w, status, body); err != nil {
		log.Error().Err(err).Msg("Failed to write response")
	}
}
