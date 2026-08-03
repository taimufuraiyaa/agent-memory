package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taimufuraiyaa/agent-memory/internal/core"
	"github.com/taimufuraiyaa/agent-memory/internal/library"
)

func (s *Store) PutLibrary(ctx context.Context, value library.Library) error {
	if err := value.Validate(); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO libraries (id, kind, owner_id, owner_kind, organization_id)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET kind = excluded.kind, owner_id = excluded.owner_id,
owner_kind = excluded.owner_kind, organization_id = excluded.organization_id
`, value.ID, value.Kind, value.Owner.ID, value.Owner.Kind, value.OrganizationID)
	if err != nil {
		return fmt.Errorf("put library: %w", err)
	}
	return nil
}

func (s *Store) PutMembership(ctx context.Context, membership library.Membership) error {
	if err := membership.Validate(); err != nil {
		return err
	}
	capabilities, err := json.Marshal(membership.Capabilities)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO library_memberships (library_id, principal_id, capabilities_json, version, active)
VALUES (?, ?, ?, ?, 1)
ON CONFLICT(library_id, principal_id) DO UPDATE SET capabilities_json = excluded.capabilities_json,
version = excluded.version, active = 1
`, membership.LibraryID, membership.PrincipalID, string(capabilities), membership.Version)
	if err != nil {
		return fmt.Errorf("put membership: %w", err)
	}
	return nil
}

func (s *Store) RemoveMembership(ctx context.Context, libraryID, principalID, version string) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE library_memberships SET active = 0, version = ? WHERE library_id = ? AND principal_id = ?
`, version, libraryID, principalID)
	if err != nil {
		return fmt.Errorf("remove membership: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return errors.New("membership not found")
	}
	return nil
}

func (s *Store) GetActiveMembership(ctx context.Context, libraryID, principalID string) (library.Membership, bool, error) {
	var membership library.Membership
	var capabilitiesJSON string
	err := s.db.QueryRowContext(ctx, `
SELECT library_id, principal_id, capabilities_json, version, active
FROM library_memberships WHERE library_id = ? AND principal_id = ? AND active = 1
`, libraryID, principalID).Scan(&membership.LibraryID, &membership.PrincipalID, &capabilitiesJSON, &membership.Version, &membership.Active)
	if errors.Is(err, sql.ErrNoRows) {
		return library.Membership{}, false, nil
	}
	if err != nil {
		return library.Membership{}, false, fmt.Errorf("get membership: %w", err)
	}
	if err := json.Unmarshal([]byte(capabilitiesJSON), &membership.Capabilities); err != nil {
		return library.Membership{}, false, fmt.Errorf("decode membership capabilities: %w", err)
	}
	return membership, true, nil
}

func (s *Store) PutLibraryResourcePolicy(ctx context.Context, resource library.LibraryResourcePolicy) error {
	if err := resource.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(resource.Policy)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO library_resources (library_id, resource_type, resource_id, policy_json)
VALUES (?, ?, ?, ?)
ON CONFLICT(resource_type, resource_id) DO UPDATE SET library_id = excluded.library_id, policy_json = excluded.policy_json
`, resource.LibraryID, resource.ResourceType, resource.ResourceID, string(payload))
	if err != nil {
		return fmt.Errorf("put library resource policy: %w", err)
	}
	return nil
}

func (s *Store) GetLibraryResourcePolicy(ctx context.Context, resourceType library.ResourceType, resourceID string) (library.LibraryResourcePolicy, error) {
	var resource library.LibraryResourcePolicy
	var payload string
	err := s.db.QueryRowContext(ctx, `
SELECT library_id, resource_type, resource_id, policy_json
FROM library_resources WHERE resource_type = ? AND resource_id = ?
`, resourceType, resourceID).Scan(&resource.LibraryID, &resource.ResourceType, &resource.ResourceID, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return library.LibraryResourcePolicy{}, errors.New("library resource not found")
	}
	if err != nil {
		return library.LibraryResourcePolicy{}, fmt.Errorf("get library resource policy: %w", err)
	}
	if err := json.Unmarshal([]byte(payload), &resource.Policy); err != nil {
		return library.LibraryResourcePolicy{}, fmt.Errorf("decode library resource policy: %w", err)
	}
	return resource, nil
}

func (s *Store) ListLibraryResourcePolicies(ctx context.Context, resourceType library.ResourceType) ([]library.LibraryResourcePolicy, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT library_id, resource_type, resource_id, policy_json
FROM library_resources WHERE resource_type = ? ORDER BY resource_id
`, resourceType)
	if err != nil {
		return nil, fmt.Errorf("list library resource policies: %w", err)
	}
	defer func() { _ = rows.Close() }()
	resources := []library.LibraryResourcePolicy{}
	for rows.Next() {
		var resource library.LibraryResourcePolicy
		var payload string
		if err := rows.Scan(&resource.LibraryID, &resource.ResourceType, &resource.ResourceID, &payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &resource.Policy); err != nil {
			return nil, err
		}
		resources = append(resources, resource)
	}
	return resources, rows.Err()
}

func MembershipScope(principal core.Principal, membership library.Membership) core.AuthorizationScope {
	return core.AuthorizationScope{
		Principal: principal, Capabilities: membership.Capabilities, PolicyVersion: membership.Version,
	}
}
