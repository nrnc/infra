package data

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"gorm.io/gorm"
	"gotest.tools/v3/assert"

	"github.com/infrahq/infra/internal"
	"github.com/infrahq/infra/internal/logging"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/internal/testing/database"
	"github.com/infrahq/infra/internal/testing/patch"
	"github.com/infrahq/infra/uid"
)

func setupDB(t *testing.T, driver gorm.Dialector) *DB {
	t.Helper()
	patch.ModelsSymmetricKey(t)

	db, err := NewDB(driver, NewDBOptions{})
	assert.NilError(t, err)

	logging.PatchLogger(t, zerolog.NewTestWriter(t))

	return db
}

func txnForTestCase(t *testing.T, db *DB, orgID uid.ID) *Transaction {
	t.Helper()
	tx, err := db.Begin(context.Background())
	assert.NilError(t, err)
	t.Cleanup(func() {
		_ = tx.Rollback()
	})
	return tx.WithOrgID(orgID)
}

// runDBTests against all supported databases.
// Set POSTGRESQL_CONNECTION to a postgresql connection string to run tests
// against postgresql.
func runDBTests(t *testing.T, run func(t *testing.T, db *DB)) {
	t.Run("postgres", func(t *testing.T) {
		pgsql := database.PostgresDriver(t, "")
		db := setupDB(t, pgsql.Dialector)
		run(t, db)
		db.Rollback()
	})
}

func TestSnowflakeIDSerialization(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		id := uid.New()
		g := &models.Group{Model: models.Model{ID: id}, Name: "Foo"}
		err := db.Create(g).Error
		assert.NilError(t, err)

		var group models.Group
		err = db.First(&group, &models.Group{Name: "Foo"}).Error
		assert.NilError(t, err)
		assert.Assert(t, 0 != group.ID)

		var intID int64
		err = db.Select("id").Table("groups").Scan(&intID).Error
		assert.NilError(t, err)

		assert.Equal(t, int64(id), intID)
	})
}

func TestPaginationSelector(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		alphabeticalIdentities := []string{}
		for r := 'a'; r < 'a'+26; r++ {
			alphabeticalIdentities = append(alphabeticalIdentities, string(r))
			g := &models.Identity{Name: string(r)}
			assert.NilError(t, CreateIdentity(db, g))
		}

		p := Pagination{Page: 1, Limit: 10}

		actual, err := ListIdentities(db, &p, NotName(models.InternalInfraConnectorIdentityName))
		assert.NilError(t, err)
		assert.Equal(t, len(actual), 10)
		for i := 0; i < p.Limit; i++ {
			if actual[i].Name == models.InternalInfraConnectorIdentityName {
				continue
			}
			assert.Equal(t, alphabeticalIdentities[i+(p.Page-1)*p.Limit], actual[i].Name)
		}

		p.Page = 2
		actual, err = ListIdentities(db, &p, NotName(models.InternalInfraConnectorIdentityName))
		assert.NilError(t, err)
		assert.Equal(t, len(actual), 10)
		for i := 0; i < p.Limit; i++ {
			assert.Equal(t, alphabeticalIdentities[i+(p.Page-1)*p.Limit], actual[i].Name)
		}

		p.Page = 3
		actual, err = ListIdentities(db, &p, NotName(models.InternalInfraConnectorIdentityName))
		assert.NilError(t, err)
		assert.Equal(t, len(actual), 6)

		for i := 0; i < 6; i++ {
			assert.Equal(t, alphabeticalIdentities[i+(p.Page-1)*p.Limit], actual[i].Name)
		}

		p.Page, p.Limit = 1, 26
		actual, err = ListIdentities(db, &p, NotName(models.InternalInfraConnectorIdentityName))
		assert.NilError(t, err)
		for i, user := range actual {
			assert.Equal(t, user.Name, alphabeticalIdentities[i])
		}
	})
}

func TestDefaultSortFromType(t *testing.T) {
	assert.Equal(t, getDefaultSortFromType(new(models.AccessKey)), "name ASC")
	assert.Equal(t, getDefaultSortFromType(new(models.Destination)), "name ASC")
	assert.Equal(t, getDefaultSortFromType(new(models.Grant)), "id ASC")
	assert.Equal(t, getDefaultSortFromType(new(models.Group)), "name ASC")
	assert.Equal(t, getDefaultSortFromType(new(models.Provider)), "name ASC")
	assert.Equal(t, getDefaultSortFromType(new(models.Identity)), "name ASC")
}

func TestCreateTransactionError(t *testing.T) {
	// on creation error (such as conflict) the database transaction should still be usable
	runDBTests(t, func(t *testing.T, db *DB) {
		err := db.Transaction(func(txDB *gorm.DB) error {
			tx := &Transaction{DB: txDB, orgID: 12345}

			g := &models.Grant{}
			err := add(tx, g)
			if err != nil {
				return err
			}

			// attempt to re-create, which results in a conflict
			err = add(tx, g)
			assert.ErrorContains(t, err, "already exists")

			// the same transaction should still be usable
			_, err = get[models.Grant](tx, ByID(g.ID))
			return err
		})

		assert.NilError(t, err)
	})
}

func TestSetOrg(t *testing.T) {
	model := &models.AccessKey{}

	tx := &Transaction{orgID: 123456}
	setOrg(tx, model)
	assert.Equal(t, model.OrganizationID, uid.ID(123456))
}

func TestNewDB(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		assert.Equal(t, db.DefaultOrg.ID, uid.ID(defaultOrganizationID))

		org, err := GetOrganization(db, ByID(defaultOrganizationID))
		assert.NilError(t, err)
		assert.DeepEqual(t, org, db.DefaultOrg, cmpTimeWithDBPrecision)
	})
}

func TestDB_Begin(t *testing.T) {
	runDBTests(t, func(t *testing.T, db *DB) {
		t.Run("rollback", func(t *testing.T) {
			ctx := context.Background()
			tx, err := db.Begin(ctx)
			assert.NilError(t, err)
			tx = tx.WithOrgID(db.DefaultOrg.ID)

			user := &models.Identity{Name: "something@example.com"}
			err = CreateIdentity(tx, user)
			assert.NilError(t, err)

			assert.NilError(t, tx.Rollback())

			// using the tx fails
			_, err = GetIdentity(tx, ByID(user.ID))
			assert.ErrorContains(t, err, "transaction has already been committed or rolled back")

			// using the db shows to show the rollback worked
			_, err = GetIdentity(db, ByID(user.ID))
			assert.ErrorIs(t, err, internal.ErrNotFound)
		})
		t.Run("commit", func(t *testing.T) {
			ctx := context.Background()
			tx, err := db.Begin(ctx)
			assert.NilError(t, err)
			tx = tx.WithOrgID(db.DefaultOrg.ID)

			user := &models.Identity{Name: "something@example.com"}
			err = CreateIdentity(tx, user)
			assert.NilError(t, err)

			assert.NilError(t, tx.Commit())

			// using the tx fails
			_, err = GetIdentity(tx, ByID(user.ID))
			assert.ErrorContains(t, err, "transaction has already been committed or rolled back")

			// using the db shows the commit worked
			_, err = GetIdentity(db, ByID(user.ID))
			assert.NilError(t, err)
		})
	})
}
