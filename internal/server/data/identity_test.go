package data

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gotest.tools/v3/assert"

	"github.com/infrahq/infra/internal"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

var cmpModelsGroupShallow = cmp.Comparer(func(x, y models.Group) bool {
	return x.Name == y.Name && x.OrganizationID == y.OrganizationID
})

func TestIdentity(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		bond := models.Identity{Name: "jbond@infrahq.com"}

		err := db.Create(&bond).Error
		assert.NilError(t, err)

		var identity models.Identity
		err = db.First(&identity, &models.Identity{Name: bond.Name}).Error
		assert.NilError(t, err)
		assert.Assert(t, 0 != identity.ID)
		assert.Equal(t, bond.Name, identity.Name)
		assert.Assert(t, len(identity.VerificationToken) > 0, "verification token must be set")
	})
}

func createIdentities(t *testing.T, db GormTxn, identities ...*models.Identity) {
	t.Helper()
	for i := range identities {
		err := CreateIdentity(db, identities[i])
		assert.NilError(t, err, identities[i].Name)
	}
}

func TestCreateDuplicateUser(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		var (
			bond   = models.Identity{Name: "jbond@infrahq.com"}
			bourne = models.Identity{Name: "jbourne@infrahq.com"}
			bauer  = models.Identity{Name: "jbauer@infrahq.com"}
		)

		createIdentities(t, db, &bond, &bourne, &bauer)

		b := bond
		b.ID = 0
		err := CreateIdentity(db, &b)
		assert.ErrorContains(t, err, "a user with that name already exists")
	})
}

func TestGetIdentity(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		var (
			bond   = models.Identity{Name: "jbond@infrahq.com"}
			bourne = models.Identity{Name: "jbourne@infrahq.com"}
			bauer  = models.Identity{Name: "jbauer@infrahq.com"}
		)

		createIdentities(t, db, &bond, &bourne, &bauer)

		identity, err := GetIdentity(db, ByName(bond.Name))
		assert.NilError(t, err)
		assert.Assert(t, 0 != identity.ID)
	})
}

func TestListIdentities(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		var (
			everyone  = models.Group{Name: "Everyone"}
			engineers = models.Group{Name: "Engineering"}
			product   = models.Group{Name: "Product"}
		)
		createGroups(t, db, &everyone, &engineers, &product)

		var (
			bond = models.Identity{
				Name:   "jbond@infrahq.com",
				Groups: []models.Group{everyone, engineers},
			}
			bourne = models.Identity{
				Name:   "jbourne@infrahq.com",
				Groups: []models.Group{everyone, product},
			}
			bauer = models.Identity{
				Name:   "jbauer@infrahq.com",
				Groups: []models.Group{everyone},
			}
		)
		createIdentities(t, db, &bond, &bourne, &bauer)

		connector := InfraConnectorIdentity(db)

		t.Run("list all", func(t *testing.T) {
			identities, err := ListIdentities(db, nil)
			assert.NilError(t, err)
			expected := []models.Identity{*connector, bauer, bond, bourne}
			assert.DeepEqual(t, identities, expected, cmpModelsIdentityShallow)
		})

		t.Run("filter by name", func(t *testing.T) {
			identities, err := ListIdentities(db, nil, ByName(bourne.Name))
			assert.NilError(t, err)
			expected := []models.Identity{bourne}
			assert.DeepEqual(t, identities, expected, cmpModelsIdentityShallow)
		})

		t.Run("filter identities by group", func(t *testing.T) {
			actual, err := ListIdentities(db, nil, ByOptionalIdentityGroupID(everyone.ID))
			assert.NilError(t, err)
			expected := []models.Identity{bauer, bond, bourne}
			assert.DeepEqual(t, actual, expected, cmpModelsIdentityShallow)
		})

		t.Run("filter identities by different group", func(t *testing.T) {
			actual, err := ListIdentities(db, nil, ByOptionalIdentityGroupID(engineers.ID))
			assert.NilError(t, err)
			expected := []models.Identity{bond}
			assert.DeepEqual(t, actual, expected, cmpModelsIdentityShallow)
		})

		t.Run("filter identities by group and name", func(t *testing.T) {
			actual, err := ListIdentities(db, nil, ByOptionalIdentityGroupID(everyone.ID), ByName(bauer.Name))
			assert.NilError(t, err)
			expected := []models.Identity{bauer}
			assert.DeepEqual(t, actual, expected, cmpModelsIdentityShallow)
		})
	})
}

var cmpModelsIdentityShallow = cmp.Comparer(func(x, y models.Identity) bool {
	return x.Name == y.Name
})

func TestDeleteIdentity(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		var (
			bond   = models.Identity{Name: "jbond@infrahq.com"}
			bourne = models.Identity{Name: "jbourne@infrahq.com"}
			bauer  = models.Identity{Name: "jbauer@infrahq.com"}
		)

		createIdentities(t, db, &bond, &bourne, &bauer)

		_, err := GetIdentity(db, ByName(bond.Name))
		assert.NilError(t, err)

		err = DeleteIdentities(db, ByName(bond.Name))
		assert.NilError(t, err)

		_, err = GetIdentity(db, ByName(bond.Name))
		assert.Error(t, err, "record not found")

		// deleting a nonexistent identity should not fail
		err = DeleteIdentities(db, ByName(bond.Name))
		assert.NilError(t, err)

		// deleting a identity should not delete unrelated identities
		_, err = GetIdentity(db, ByName(bourne.Name))
		assert.NilError(t, err)
	})
}

func TestDeleteIdentityWithGroups(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		var (
			bond   = models.Identity{Name: "jbond@infrahq.com"}
			bourne = models.Identity{Name: "jbourne@infrahq.com"}
			bauer  = models.Identity{Name: "jbauer@infrahq.com"}
		)
		group := &models.Group{Name: "Agents"}
		err := CreateGroup(db, group)
		assert.NilError(t, err)

		createIdentities(t, db, &bond, &bourne, &bauer)

		err = AddUsersToGroup(db, group.ID, []uid.ID{bond.ID, bourne.ID, bauer.ID})
		assert.NilError(t, err)

		err = DeleteIdentities(db, ByName(bond.Name))
		assert.NilError(t, err)

		group, err = GetGroup(db, ByID(group.ID))
		assert.NilError(t, err)
		assert.Equal(t, group.TotalUsers, 2)
	})
}

func TestReCreateIdentitySameName(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		var (
			bond   = models.Identity{Name: "jbond@infrahq.com"}
			bourne = models.Identity{Name: "jbourne@infrahq.com"}
			bauer  = models.Identity{Name: "jbauer@infrahq.com"}
		)

		createIdentities(t, db, &bond, &bourne, &bauer)

		err := DeleteIdentities(db, ByName(bond.Name))
		assert.NilError(t, err)

		err = CreateIdentity(db, &models.Identity{Name: bond.Name})
		assert.NilError(t, err)
	})
}

func TestAssignIdentityToGroups(t *testing.T) {
	tests := []struct {
		Name           string
		StartingGroups []string       // groups identity starts with
		ExistingGroups []string       // groups from last provider sync
		IncomingGroups []string       // groups from this provider sync
		ExpectedGroups []models.Group // groups identity should have at end
	}{
		{
			Name:           "test where the provider is trying to add a group the identity doesn't have elsewhere",
			StartingGroups: []string{"foo"},
			ExistingGroups: []string{},
			IncomingGroups: []string{"foo2"},
			ExpectedGroups: []models.Group{
				{
					Name: "foo",
					OrganizationMember: models.OrganizationMember{
						OrganizationID: 1000,
					},
				},
				{
					Name: "foo2",
					OrganizationMember: models.OrganizationMember{
						OrganizationID: 1000,
					},
				},
			},
		},
		{
			Name:           "test where the provider is trying to add a group the identity has from elsewhere",
			StartingGroups: []string{"foo"},
			ExistingGroups: []string{},
			IncomingGroups: []string{"foo", "foo2"},
			ExpectedGroups: []models.Group{
				{
					Name: "foo",
					OrganizationMember: models.OrganizationMember{
						OrganizationID: 1000,
					},
				},
				{
					Name: "foo2",
					OrganizationMember: models.OrganizationMember{
						OrganizationID: 1000,
					},
				},
			},
		},
		{
			Name:           "test where the group with the same name exists in another org",
			StartingGroups: []string{},
			ExistingGroups: []string{},
			IncomingGroups: []string{"Everyone"},
			ExpectedGroups: []models.Group{
				{
					Name: "Everyone",
					OrganizationMember: models.OrganizationMember{
						OrganizationID: 1000,
					},
				},
			},
		},
	}

	runDBTests(t, func(t *testing.T, db *DB) {
		otherOrg := &models.Organization{Name: "Other", Domain: "other.example.org"}
		assert.NilError(t, CreateOrganization(db, otherOrg))
		tx := txnForTestCase(t, db, otherOrg.ID)
		group := &models.Group{Name: "Everyone"}
		assert.NilError(t, CreateGroup(tx, group))
		assert.NilError(t, tx.Commit())

		for i, test := range tests {
			t.Run(test.Name, func(t *testing.T) {
				// setup identity
				identity := &models.Identity{Name: fmt.Sprintf("foo+%d@example.com", i)}
				err := CreateIdentity(db, identity)
				assert.NilError(t, err)

				// setup identity's groups
				for _, gn := range test.StartingGroups {
					g, err := GetGroup(db, ByName(gn))
					if errors.Is(err, internal.ErrNotFound) {
						g = &models.Group{Name: gn}
						err = CreateGroup(db, g)
					}
					assert.NilError(t, err)
					identity.Groups = append(identity.Groups, *g)
				}
				err = SaveIdentity(db, identity)
				assert.NilError(t, err)

				// setup providerUser record
				provider := InfraProvider(db)
				pu, err := CreateProviderUser(db, provider, identity)
				assert.NilError(t, err)

				pu.Groups = test.ExistingGroups
				err = UpdateProviderUser(db, pu)
				assert.NilError(t, err)

				err = AssignIdentityToGroups(db, identity, provider, test.IncomingGroups)
				assert.NilError(t, err)

				// check the result
				actual, err := ListGroups(db, nil, ByGroupMember(identity.ID))
				assert.NilError(t, err)

				assert.DeepEqual(t, actual, test.ExpectedGroups, cmpModelsGroupShallow)
			})
		}
	})
}
