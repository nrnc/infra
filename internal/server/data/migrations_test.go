package data

import (
	"bytes"
	"database/sql"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v4"
	"github.com/rs/zerolog"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
	"gotest.tools/v3/golden"

	"github.com/infrahq/infra/internal/logging"
	"github.com/infrahq/infra/internal/server/data/migrator"
	"github.com/infrahq/infra/internal/server/data/schema"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/internal/testing/database"
	"github.com/infrahq/infra/internal/testing/patch"
	"github.com/infrahq/infra/uid"
)

func TestMigrations(t *testing.T) {
	if testing.Short() {
		t.Skip("too slow for -short run")
	}
	patch.ModelsSymmetricKey(t)
	allMigrations := migrations()

	type testCase struct {
		label    testCaseLabel
		setup    func(t *testing.T, db WriteTxn)
		expected func(t *testing.T, db WriteTxn)
		cleanup  func(t *testing.T, db WriteTxn)
	}

	run := func(t *testing.T, index int, tc testCase, db *DB) {
		logging.PatchLogger(t, zerolog.NewTestWriter(t))
		if index >= len(allMigrations) {
			t.Fatalf("there are more test cases than migrations")
		}
		mgs := allMigrations[:index+1]

		if mID := mgs[len(mgs)-1].ID; mID != tc.label.Name {
			t.Error("the list of test cases is not in the same order as the list of migrations")
			t.Fatalf("test case %v was run with migration ID %v", tc.label.Name, mID)
		}

		if index == 0 {
			filename := fmt.Sprintf("testdata/migrations/%v-%v.sql", tc.label.Name, db.Dialector.Name())
			raw, err := ioutil.ReadFile(filename)
			assert.NilError(t, err)

			_, err = db.Exec(string(raw))
			assert.NilError(t, err)
		}

		if tc.setup != nil {
			tc.setup(t, db)
		}
		if tc.cleanup != nil {
			defer tc.cleanup(t, db)
		}

		opts := migrator.Options{
			InitSchema: func(db migrator.DB) error {
				return fmt.Errorf("unexpected call to init schema")
			},
		}

		m := migrator.New(db, opts, mgs)
		err := m.Migrate()
		assert.NilError(t, err)

		tc.expected(t, db)
	}

	testCases := []testCase{
		{
			label: testCaseLine("202204281130"),
			expected: func(t *testing.T, tx WriteTxn) {
				// dropped columns are tested by schema comparison
			},
		},
		{
			label: testCaseLine("202204291613"),
			expected: func(t *testing.T, db WriteTxn) {
				// dropped columns are tested by schema comparison
			},
		},
		{
			label: testCaseLine("2022-06-08T10:27-fixed"),
			expected: func(t *testing.T, db WriteTxn) {
				// dropped constraints are tested by schema comparison
			},
		},
		{
			label: testCaseLine("202206151027"),
			setup: func(t *testing.T, db WriteTxn) {
				stmt := `INSERT INTO providers(name) VALUES ('infra'), ('okta');`
				_, err := db.Exec(stmt)
				assert.NilError(t, err)
			},
			cleanup: func(t *testing.T, db WriteTxn) {
				stmt := `DELETE FROM providers`
				_, err := db.Exec(stmt)
				assert.NilError(t, err)
			},
			expected: func(t *testing.T, db WriteTxn) {
				type provider struct {
					Name string
					Kind models.ProviderKind
				}

				query := `SELECT name, kind FROM providers where deleted_at is null`
				var actual []provider
				rows, err := db.Query(query)
				assert.NilError(t, err)

				for rows.Next() {
					var p provider
					err := rows.Scan(&p.Name, &p.Kind)
					assert.NilError(t, err)
					actual = append(actual, p)
				}

				expected := []provider{
					{Name: "infra", Kind: models.ProviderKindInfra},
					{Name: "okta", Kind: models.ProviderKindOkta},
				}
				assert.DeepEqual(t, actual, expected)
			},
		},
		{
			label: testCaseLine("202206161733"),
			setup: func(t *testing.T, db WriteTxn) {
				// integrity check
				assert.Assert(t, migrator.HasTable(db, "trusted_certificates"))
				assert.Assert(t, migrator.HasTable(db, "root_certificates"))
			},
			expected: func(t *testing.T, db WriteTxn) {
				assert.Assert(t, !migrator.HasTable(db, "trusted_certificates"))
				assert.Assert(t, !migrator.HasTable(db, "root_certificates"))
			},
		},
		{
			label: testCaseLine("202206281027"),
			setup: func(t *testing.T, db WriteTxn) {
				stmt := `
INSERT INTO providers (id, created_at, updated_at, deleted_at, name, url, client_id, client_secret, kind, created_by) VALUES (67301777540980736, '2022-07-05 17:13:14.172568+00', '2022-07-05 17:13:14.172568+00', NULL, 'infra', '', '', 'AAAAEIRG2/PYF2erJG6cYHTybucGYWVzZ2NtBDjJTEEbL3Jvb3QvLmluZnJhL3NxbGl0ZTMuZGIua2V5DGt4MdtlZuxOUhZQTw', 'infra', 1);
INSERT INTO providers (id, created_at, updated_at, deleted_at, name, url, client_id, client_secret, kind, created_by) VALUES (67301777540980737, '2022-07-05 17:13:14.172568+00', '2022-07-05 17:13:14.172568+00', NULL, 'okta', 'example.okta.com', 'client-id', 'AAAAEIRG2/PYF2erJG6cYHTybucGYWVzZ2NtBDjJTEEbL3Jvb3QvLmluZnJhL3NxbGl0ZTMuZGIua2V5DGt4MdtlZuxOUhZQTw', 'okta', 1);
`
				_, err := db.Exec(stmt)
				assert.NilError(t, err)
			},
			cleanup: func(t *testing.T, db WriteTxn) {
				_, err := db.Exec(`DELETE FROM providers;`)
				assert.NilError(t, err)
			},
			expected: func(t *testing.T, db WriteTxn) {
				rows, err := db.Query(`SELECT name, auth_url, scopes FROM providers ORDER BY name`)
				assert.NilError(t, err)

				var actual []models.Provider
				for rows.Next() {
					var p models.Provider
					var authURL sql.NullString
					err := rows.Scan(&p.Name, &authURL, &p.Scopes)
					assert.NilError(t, err)
					p.AuthURL = authURL.String
					actual = append(actual, p)
				}

				expected := []models.Provider{
					{
						Name:    "infra",
						AuthURL: "",
						Scopes:  nil,
					},
					{
						Name:    "okta",
						AuthURL: "https://example.okta.com/oauth2/v1/authorize", // set from external endpoint
						Scopes:  models.CommaSeparatedStrings{"openid", "email", "offline_access", "groups"},
					},
				}
				assert.DeepEqual(t, actual, expected)
			},
		},
		{
			label: testCaseLine("202207041724"),
			setup: func(t *testing.T, db WriteTxn) {
				stmt := `
INSERT INTO destinations (id, created_at, updated_at, name, unique_id)
VALUES (12345, '2022-07-05 00:41:49.143574', '2022-07-05 01:41:49.143574', 'the-destination', 'unique-id');`
				_, err := db.Exec(stmt)
				assert.NilError(t, err)
			},
			cleanup: func(t *testing.T, db WriteTxn) {
				_, err := db.Exec(`DELETE FROM destinations`)
				assert.NilError(t, err)
			},
			expected: func(t *testing.T, db WriteTxn) {
				stmt := `SELECT id, name, updated_at, last_seen_at from destinations`
				rows, err := db.Query(stmt)
				assert.NilError(t, err)
				defer rows.Close()

				var actual []models.Destination
				for rows.Next() {
					var d models.Destination
					err := rows.Scan(&d.ID, &d.Name, &d.UpdatedAt, &d.LastSeenAt)
					assert.NilError(t, err)
					actual = append(actual, d)
				}

				updated := parseTime(t, "2022-07-05T01:41:49.143574Z")
				expected := []models.Destination{
					{
						Model: models.Model{
							ID:        12345,
							UpdatedAt: updated,
						},
						Name:       "the-destination",
						LastSeenAt: updated,
					},
				}
				assert.DeepEqual(t, actual, expected)
			},
		},
		{
			label: testCaseLine("202207081217"),
			setup: func(t *testing.T, db WriteTxn) {
				stmt := `
					INSERT INTO grants(id, subject, resource, privilege)
					VALUES (10100, 'i:aaa', 'infra', 'admin'),
					       (10101, 'i:aaa', 'infra', 'admin'),
					       (10102, 'i:aaa', 'other', 'admin'),
					       (10103, 'i:aaa', 'infra', 'view'),
						   (10104, 'i:aab', 'infra', 'admin');
				`
				_, err := db.Exec(stmt)
				assert.NilError(t, err)
			},
			cleanup: func(t *testing.T, db WriteTxn) {
				_, err := db.Exec(`DELETE FROM grants`)
				assert.NilError(t, err)
			},
			expected: func(t *testing.T, db WriteTxn) {
				stmt := `SELECT id, subject, resource, privilege FROM grants`
				rows, err := db.Query(stmt)
				assert.NilError(t, err)
				defer rows.Close()

				var actual []models.Grant
				for rows.Next() {
					var g models.Grant
					err := rows.Scan(&g.ID, &g.Subject, &g.Resource, &g.Privilege)
					assert.NilError(t, err)
					actual = append(actual, g)
				}

				expected := []models.Grant{
					{
						Model:     models.Model{ID: 10100},
						Subject:   "i:aaa",
						Resource:  "infra",
						Privilege: "admin",
					},
					{
						Model:     models.Model{ID: 10102},
						Subject:   "i:aaa",
						Resource:  "other",
						Privilege: "admin",
					},
					{
						Model:     models.Model{ID: 10103},
						Subject:   "i:aaa",
						Resource:  "infra",
						Privilege: "view",
					},
					{
						Model:     models.Model{ID: 10104},
						Subject:   "i:aab",
						Resource:  "infra",
						Privilege: "admin",
					},
				}
				assert.DeepEqual(t, actual, expected)
			},
		},
		{
			label: testCaseLine("202207270000"),
			setup: func(t *testing.T, db WriteTxn) {
				stmt := `
INSERT INTO provider_users (identity_id, provider_id, id, created_at, updated_at, deleted_at, email, groups, last_update, redirect_url, access_token, refresh_token, expires_at) VALUES(75225930155761664,75225930151567361,75226263837810687,'2022-07-27 14:02:18.934641547+00:00','2022-07-27 14:02:19.547474589+00:00',NULL,'example@infrahq.com','','2022-07-27 14:02:19.54741888+00:00','http://localhost:8301','aaa','bbb','2022-07-27 15:02:18.420551838+00:00');
INSERT INTO provider_users (identity_id, provider_id, id, created_at, updated_at, deleted_at, email, groups, last_update, redirect_url, access_token, refresh_token, expires_at) VALUES(75225930155761664,75225930151567360,75226263837810688,'2022-07-27 14:02:18.934641547+00:00','2022-07-27 14:02:19.547474589+00:00','2022-07-27 14:00:59.448457344+00:00','example@infrahq.com','','2022-07-27 14:02:19.54741888+00:00','http://localhost:8301','aaa','bbb','2022-07-27 15:02:18.420551838+00:00');
`
				_, err := db.Exec(stmt)
				assert.NilError(t, err)
			},
			cleanup: func(t *testing.T, db WriteTxn) {
				_, err := db.Exec(`DELETE FROM provider_users;`)
				assert.NilError(t, err)
			},
			expected: func(t *testing.T, db WriteTxn) {
				// there should only be one provider user from the infra provider
				// the other user has a deleted_at time and was cleared
				type providerUserDetails struct {
					Email      string
					ProviderID string
				}

				var puDetails []providerUserDetails
				rows, err := db.Query("SELECT email, provider_id FROM provider_users")
				assert.NilError(t, err)

				for rows.Next() {
					var u providerUserDetails
					assert.NilError(t, rows.Scan(&u.Email, &u.ProviderID))
					puDetails = append(puDetails, u)
				}
				assert.NilError(t, rows.Close())

				assert.Equal(t, len(puDetails), 1)
				assert.Equal(t, puDetails[0].Email, "example@infrahq.com")
				assert.Equal(t, puDetails[0].ProviderID, "75225930151567361")
			},
		},
		{
			label: testCaseLine("2022-07-28T12:46"),
			setup: func(t *testing.T, db WriteTxn) {
				stmt := `
				INSERT INTO identities (id, name, deleted_at) VALUES (100, 'deleted@test.com', '2022-07-27 14:02:18.934641547+00:00'), (101, 'user@test.com', NULL);
				INSERT INTO groups (id, name) VALUES (102, 'Test');
				INSERT INTO identities_groups (identity_id, group_id) VALUES (100, 102), (101, 102);`

				_, err := db.Exec(stmt)
				assert.NilError(t, err)
			},
			expected: func(t *testing.T, db WriteTxn) {
				type IdentityGroup struct {
					IdentityID uid.ID
					GroupID    uid.ID
				}
				var relations []IdentityGroup
				rows, err := db.Query("SELECT identity_id, group_id FROM identities_groups")
				assert.NilError(t, err)
				defer rows.Close()

				for rows.Next() {
					var relation IdentityGroup
					err := rows.Scan(&relation.IdentityID, &relation.GroupID)
					assert.NilError(t, err)
					relations = append(relations, relation)
				}

				assert.Equal(t, len(relations), 1)
				assert.DeepEqual(t, relations[0], IdentityGroup{IdentityID: 101, GroupID: 102})
			},
			cleanup: func(t *testing.T, db WriteTxn) {
				_, err := db.Exec(`DELETE FROM identities_groups;`)
				assert.NilError(t, err)
				_, err = db.Exec(`DELETE FROM identities;`)
				assert.NilError(t, err)
				_, err = db.Exec(`DELETE FROM groups;`)
				assert.NilError(t, err)
			},
		},
		{
			label: testCaseLine("2022-07-21T18:28"),
			setup: func(t *testing.T, db WriteTxn) {
				_, err := db.Exec(`INSERT INTO settings(id, created_at) VALUES(1, ?);`, time.Now())
				assert.NilError(t, err)
			},
			cleanup: func(t *testing.T, db WriteTxn) {
				_, err := db.Exec(`DELETE FROM settings WHERE id=1;`)
				assert.NilError(t, err)
			},
			expected: func(t *testing.T, db WriteTxn) {
				row := db.QueryRow(`
					SELECT lowercase_min, uppercase_min, number_min, symbol_min, length_min
					FROM settings
					LIMIT 1
				`)

				var settings models.Settings
				err := row.Scan(
					&settings.LowercaseMin,
					&settings.UppercaseMin,
					&settings.NumberMin,
					&settings.SymbolMin,
					&settings.LengthMin,
				)
				assert.NilError(t, err)
				expected := models.Settings{LengthMin: 8}
				assert.DeepEqual(t, settings, expected)
			},
		},
		{
			label: testCaseLine("2022-07-27T15:54"),
			expected: func(t *testing.T, db WriteTxn) {
				// column changes are tested with schema comparison
			},
		},
		{
			label: testCaseLine("2022-08-04T17:72"),
			expected: func(t *testing.T, db WriteTxn) {
				// schema changes are tested with schema comparison
			},
		},
		{
			label: testCaseLine("2022-08-10T13:35"),
			setup: func(t *testing.T, db WriteTxn) {
				stmt := `
INSERT INTO providers(id, name) VALUES (12345, 'okta');
INSERT INTO settings(id) VALUES(24567);
`
				_, err := db.Exec(stmt)
				assert.NilError(t, err)
			},
			cleanup: func(t *testing.T, db WriteTxn) {
				stmt := `
DELETE FROM providers WHERE id=12345;
DELETE FROM settings WHERE id=24567;
`
				_, err := db.Exec(stmt)
				assert.NilError(t, err)
			},
			expected: func(t *testing.T, db WriteTxn) {
				stmt := `SELECT id, name, created_at, updated_at FROM organizations`
				rows, err := db.Query(stmt)
				assert.NilError(t, err)

				var orgs []models.Organization
				for rows.Next() {
					org := models.Organization{}
					err := rows.Scan(&org.ID, &org.Name, &org.CreatedAt, &org.UpdatedAt)
					assert.NilError(t, err)
					orgs = append(orgs, org)
				}

				now := time.Now()
				expected := []models.Organization{
					{
						Model: models.Model{
							ID:        99,
							CreatedAt: now,
							UpdatedAt: now,
						},
						Name: "Default",
					},
				}
				assert.DeepEqual(t, orgs, expected, cmpModel)
				org := orgs[0]

				stmt = `SELECT id, organization_id FROM providers;`
				p := &models.Provider{}
				err = db.QueryRow(stmt).Scan(&p.ID, &p.OrganizationID)
				assert.NilError(t, err)

				expectedProvider := &models.Provider{
					Model:              models.Model{ID: 12345},
					OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
				}
				assert.DeepEqual(t, p, expectedProvider)

				stmt = `SELECT id, organization_id FROM settings;`
				s := &models.Settings{}
				err = db.QueryRow(stmt).Scan(&s.ID, &s.OrganizationID)
				assert.NilError(t, err)

				expectedSettings := &models.Settings{
					Model:              models.Model{ID: 24567},
					OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
				}
				assert.DeepEqual(t, s, expectedSettings)
			},
		},
		{
			label: testCaseLine("2022-08-11T11:52"),
			expected: func(t *testing.T, db WriteTxn) {
				// schema changes are tested with schema comparison
			},
		},
		{
			label: testCaseLine("2022-08-12T11:05"),
			expected: func(t *testing.T, tx WriteTxn) {
				// dropped indexes are tested by schema comparison
			},
		},
		{
			label: testCaseLine("2022-08-22T14:58:00Z"),
			expected: func(t *testing.T, tx WriteTxn) {
				// tested elsewhere
			},
		},
		{
			label: testCaseLine("2022-08-30T11:45"),
			setup: func(t *testing.T, db WriteTxn) {
				var originalOrgID uid.ID
				err := db.QueryRow(`SELECT id from organizations where name='Default'`).Scan(&originalOrgID)
				assert.NilError(t, err)

				stmt := ` INSERT INTO providers(id, name, organization_id) VALUES (12345, 'okta', ?)`
				_, err = db.Exec(stmt, originalOrgID)
				assert.NilError(t, err)

				stmt = `INSERT INTO settings(id, organization_id) VALUES(24567, ?); `
				_, err = db.Exec(stmt, originalOrgID)
				assert.NilError(t, err)
			},
			cleanup: func(t *testing.T, db WriteTxn) {
				stmt := `
DELETE FROM providers WHERE id=12345;
DELETE FROM settings WHERE id=24567;
`
				_, err := db.Exec(stmt)
				assert.NilError(t, err)
			},
			expected: func(t *testing.T, tx WriteTxn) {
				stmt := `SELECT id, name, domain FROM organizations`
				org := &models.Organization{}
				err := tx.QueryRow(stmt).Scan(&org.ID, &org.Name, (*nullString)(&org.Domain))
				assert.NilError(t, err)

				expected := &models.Organization{
					Model:  models.Model{ID: defaultOrganizationID},
					Domain: "",
					Name:   "Default",
				}
				assert.DeepEqual(t, org, expected)

				stmt = `SELECT id, organization_id FROM providers;`
				p := &models.Provider{}
				err = tx.QueryRow(stmt).Scan(&p.ID, &p.OrganizationID)
				assert.NilError(t, err)

				expectedProvider := &models.Provider{
					Model:              models.Model{ID: 12345},
					OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
				}
				assert.DeepEqual(t, p, expectedProvider)

				stmt = `SELECT id, organization_id FROM settings;`
				s := &models.Settings{}
				err = tx.QueryRow(stmt).Scan(&s.ID, &s.OrganizationID)
				assert.NilError(t, err)

				expectedSettings := &models.Settings{
					Model:              models.Model{ID: 24567},
					OrganizationMember: models.OrganizationMember{OrganizationID: org.ID},
				}
				assert.DeepEqual(t, s, expectedSettings)
			},
		},
		{
			label: testCaseLine("2022-09-01T15:00"),
			setup: func(t *testing.T, db WriteTxn) {
				var originalOrgID uid.ID
				err := db.QueryRow(`SELECT id from organizations where name='Default'`).Scan(&originalOrgID)
				assert.NilError(t, err)

				stmt := ` INSERT INTO identities(id, name, organization_id) VALUES (12345, 'migration1@example.com', ?)`
				_, err = db.Exec(stmt, originalOrgID)
				assert.NilError(t, err)
			},
			expected: func(t *testing.T, db WriteTxn) {
				stmt := `SELECT verification_token, verified FROM identities where id = ?`
				user := &models.Identity{}
				err := db.QueryRow(stmt, 12345).Scan(&user.VerificationToken, &user.Verified)
				assert.NilError(t, err)

				assert.Assert(t, !user.Verified)
				assert.Assert(t, len(user.VerificationToken) == 10)
			},
			cleanup: func(t *testing.T, db WriteTxn) {
				stmt := `DELETE FROM identities WHERE id=12345;`
				_, err := db.Exec(stmt)
				assert.NilError(t, err)
			},
		},
		{
			label: testCaseLine("2022-09-22T11:00"),
			setup: func(t *testing.T, tx WriteTxn) {
				orgA := uid.ID(1000)
				orgB := uid.ID(2000)

				groupA := models.Group{
					Model: models.Model{
						ID: 1001,
					},
					OrganizationMember: models.OrganizationMember{
						OrganizationID: orgA,
					},
					Name: "group A",
				}

				groupB := models.Group{
					Model: models.Model{
						ID: 1002,
					},
					OrganizationMember: models.OrganizationMember{
						OrganizationID: orgB,
					},
					Name: "group B",
				}

				identityA := models.Identity{
					Model: models.Model{
						ID: 1003,
					},
					OrganizationMember: models.OrganizationMember{
						OrganizationID: orgA,
					},
					Name: "identity A",
				}

				identityB := models.Identity{
					Model: models.Model{
						ID: 1004,
					},
					OrganizationMember: models.OrganizationMember{
						OrganizationID: orgB,
					},
					Name: "identity B",
				}

				stmt := `INSERT INTO groups(id, name, organization_id) VALUES (?, ?, ?)`
				_, err := tx.Exec(stmt, groupA.ID, groupA.Name, groupA.OrganizationID)
				assert.NilError(t, err)
				_, err = tx.Exec(stmt, groupB.ID, groupB.Name, groupB.OrganizationID)
				assert.NilError(t, err)

				stmt = `INSERT INTO identities(id, name, organization_id) VALUES (?, ?, ?)`
				_, err = tx.Exec(stmt, identityA.ID, identityA.Name, identityA.OrganizationID)
				assert.NilError(t, err)
				_, err = tx.Exec(stmt, identityB.ID, identityB.Name, identityB.OrganizationID)
				assert.NilError(t, err)

				stmt = `INSERT INTO identities_groups(identity_id, group_id) VALUES (?, ?)`
				_, err = tx.Exec(stmt, identityA.ID, groupA.ID)
				assert.NilError(t, err)
				_, err = tx.Exec(stmt, identityB.ID, groupB.ID)
				assert.NilError(t, err)
				_, err = tx.Exec(stmt, identityB.ID, groupA.ID)
				assert.NilError(t, err)
			},
			cleanup: func(t *testing.T, tx WriteTxn) {
				stmt := `
					DELETE FROM groups;
					DELETE FROM identities;
				`
				_, err := tx.Exec(stmt)
				assert.NilError(t, err)
			},
			expected: func(t *testing.T, tx WriteTxn) {
				stmt := `SELECT identity_id, group_id FROM identities_groups`
				rows, err := tx.Query(stmt)
				assert.NilError(t, err)
				for rows.Next() {
					var identityID uid.ID
					var groupID uid.ID
					err := rows.Scan(&identityID, &groupID)
					assert.NilError(t, err)

					var identityOrgID uid.ID
					err = tx.QueryRow(`SELECT organization_id FROM identities WHERE id = ?`, identityID).Scan(&identityOrgID)
					assert.NilError(t, err)

					var groupOrgID uid.ID
					err = tx.QueryRow(`SELECT organization_id FROM groups WHERE id = ?`, groupID).Scan(&groupOrgID)
					assert.NilError(t, err)

					assert.Equal(t, identityOrgID, groupOrgID)
				}
			},
		},
	}

	ids := make(map[string]struct{}, len(testCases))
	for _, tc := range testCases {
		ids[tc.label.Name] = struct{}{}
	}
	// all migrations should be covered by a test
	for _, m := range allMigrations {
		if _, exists := ids[m.ID]; !exists {
			t.Fatalf("migration ID %v is missing test coverage! Add a test case to this test.", m.ID)
		}
	}

	var initialSchema string
	runStep(t, "initial schema", func(t *testing.T) {
		patch.ModelsSymmetricKey(t)
		rawDB, err := newRawDB(database.PostgresDriver(t, "").Dialector)
		assert.NilError(t, err)

		db := &DB{DB: rawDB}
		opts := migrator.Options{InitSchema: initializeSchema}
		m := migrator.New(db, opts, nil)
		assert.NilError(t, m.Migrate())

		initialSchema = dumpSchema(t, os.Getenv("POSTGRESQL_CONNECTION"))

		_, err = db.Exec("DROP SCHEMA IF EXISTS testing CASCADE")
		assert.NilError(t, err)
	})

	db, err := newRawDB(database.PostgresDriver(t, "").Dialector)
	assert.NilError(t, err)
	for i, tc := range testCases {
		runStep(t, tc.label.Name, func(t *testing.T) {
			fmt.Printf("    %v: test case %v\n", tc.label.Line, tc.label.Name)
			run(t, i, tc, &DB{DB: db})
		})
	}

	runStep(t, "compare initial schema to migrated schema", func(t *testing.T) {
		migratedSchema := dumpSchema(t, os.Getenv("POSTGRESQL_CONNECTION"))

		if golden.FlagUpdate() {
			writeSchema(t, migratedSchema)
			return
		}
		if !assert.Check(t, is.Equal(initialSchema, migratedSchema)) {
			t.Log(`
The migrated schema does not match the initial schema in ./schema.sql.

If you just added a new migration, run the tests again with -update to apply the
changes to schema.sql:

    go test -run TestMigrations ./internal/server/data -update

If you changed schema.sql, add the missing migration to the migrations() function
in ./migrations.go, add a test case to this test, and run the tests again.
`)
		}
	})
}

func parseTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	assert.NilError(t, err)
	return v
}

// testCaseLine is motivated by this Go proposal https://github.com/golang/go/issues/52751.
// That issue has additional context about the problem this solves.
func testCaseLine(name string) testCaseLabel {
	_, file, line, ok := runtime.Caller(1)
	if !ok {
		return testCaseLabel{Name: name, Line: "unknown"}
	}
	return testCaseLabel{
		Name: name,
		Line: fmt.Sprintf("%v:%v", filepath.Base(file), line),
	}
}

type testCaseLabel struct {
	Name string
	Line string
}

var isEnvironmentCI = os.Getenv("CI") != ""

func dumpSchema(t *testing.T, conn string) string {
	t.Helper()
	if _, err := exec.LookPath("pg_dump"); err != nil {
		msg := "pg_dump is required to run this test. Install pg_dump or set $PATH to include it."
		if isEnvironmentCI {
			t.Fatalf(msg)
		}
		t.Skip(msg)
	}

	conf, err := pgx.ParseConfig(conn)
	assert.NilError(t, err, "failed to parse connection string")

	envs := os.Environ()
	addEnv := func(v string) {
		envs = append(envs, v)
	}

	if conf.Host != "" {
		addEnv("PGHOST=" + conf.Host)
	}
	if conf.Port != 0 {
		addEnv(fmt.Sprintf("PGPORT=%d", conf.Port))
	}
	if conf.User != "" {
		addEnv("PGUSER=" + conf.User)
	}
	if conf.Database != "" {
		addEnv("PGDATABASE=" + conf.Database)
	}
	if conf.Password != "" {
		addEnv("PGPASSWORD=" + conf.Password)
	}

	out := new(bytes.Buffer)
	// https://www.postgresql.org/docs/current/app-pgdump.html
	cmd := exec.Command("pg_dump", "--no-owner", "--no-tablespaces", "--schema-only", "--schema=testing")
	cmd.Env = envs
	cmd.Stdout = out
	cmd.Stderr = os.Stderr

	assert.NilError(t, cmd.Run())
	return out.String()
}

func writeSchema(t *testing.T, raw string) {
	stmts, err := schema.ParseSchema(raw)
	assert.NilError(t, err)

	var out bytes.Buffer
	out.WriteString(`-- SQL generated by TestMigrations DO NOT EDIT.
-- Instead of editing this file, add a migration to ./migrations.go and run:
--
--     go test -run TestMigrations ./internal/server/data -update
--
`)
	for _, stmt := range stmts {
		if stmt.TableName == "migrations" {
			continue
		}
		out.WriteString(stmt.Value)
	}

	t.Log("Writing new schema to schema.sql. Check 'git diff' for changes!")
	// nolint:gosec
	err = os.WriteFile("schema.sql", out.Bytes(), 0o644)
	assert.NilError(t, err)
}

type nullString string

func (n *nullString) Scan(value any) error {
	ns := &sql.NullString{}
	err := ns.Scan(value)
	*n = nullString(ns.String)
	return err
}
