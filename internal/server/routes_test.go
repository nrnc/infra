package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gotest.tools/v3/assert"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal"
	"github.com/infrahq/infra/internal/server/data"
	"github.com/infrahq/infra/internal/server/models"
	"github.com/infrahq/infra/uid"
)

func TestReadRequest_FromQuery(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)

	uri, err := url.Parse("/foo?alpha=beta")
	assert.NilError(t, err)

	c.Request = &http.Request{URL: uri, Method: "GET"}
	r := &struct {
		Alpha string `form:"alpha"`
	}{}
	err = readRequest(c, r)
	assert.NilError(t, err)

	assert.Equal(t, "beta", r.Alpha)
}

func TestReadRequest_JSON(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)

	uri, err := url.Parse("/foo")
	assert.NilError(t, err)

	body := bytes.NewBufferString(`{"alpha": "zeta"}`)
	c.Request = &http.Request{
		URL:           uri,
		Method:        "GET",
		Body:          io.NopCloser(body),
		ContentLength: int64(body.Len()),
		Header:        http.Header{"Content-Type": []string{"application/json"}},
	}
	r := &struct {
		Alpha string `json:"alpha"`
	}{}
	err = readRequest(c, r)
	assert.NilError(t, err)

	assert.Equal(t, "zeta", r.Alpha)
}

func TestReadRequest_UUIDs(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)

	uri, err := url.Parse("/foo/e4d97df2")
	assert.NilError(t, err)

	c.Request = &http.Request{URL: uri, Method: "GET"}
	c.Params = append(c.Params, gin.Param{Key: "id", Value: "e4d97df2"})
	r := &api.Resource{}
	err = readRequest(c, r)
	assert.NilError(t, err)

	assert.Equal(t, "e4d97df2", r.ID.String())
}

func TestReadRequest_Snowflake(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)

	id := uid.New()
	id2 := uid.New()

	uri, err := url.Parse(fmt.Sprintf("/foo/%s?form_id=%s", id.String(), id2.String()))
	assert.NilError(t, err)

	c.Request = &http.Request{URL: uri, Method: "GET"}
	c.Params = append(c.Params, gin.Param{Key: "id", Value: id.String()})
	r := &struct {
		ID     uid.ID `uri:"id"`
		FormID uid.ID `form:"form_id"`
	}{}
	err = readRequest(c, r)
	assert.NilError(t, err)

	assert.Equal(t, id, r.ID)
	assert.Equal(t, id2, r.FormID)
}

func TestReadRequest_EmptyRequest(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)

	uri, err := url.Parse("/foo")
	assert.NilError(t, err)

	c.Request = &http.Request{URL: uri, Method: "GET"}
	r := &api.EmptyRequest{}
	err = readRequest(c, r)
	assert.NilError(t, err)
}

func TestTimestampAndDurationSerialization(t *testing.T) {
	c, _ := gin.CreateTestContext(nil)

	uri, err := url.Parse("/foo")
	assert.NilError(t, err)

	orig := `{"deadline":"2022-03-23T17:50:59Z","extension":"1h35m0s"}`
	body := bytes.NewBufferString(orig)
	c.Request = &http.Request{
		URL:           uri,
		Method:        "GET",
		Body:          io.NopCloser(body),
		ContentLength: int64(body.Len()),
		Header:        http.Header{"Content-Type": []string{"application/json"}},
	}
	r := &struct {
		Deadline  api.Time     `json:"deadline"`
		Extension api.Duration `json:"extension"`
	}{}
	err = readRequest(c, r)
	assert.NilError(t, err)

	expected := time.Date(2022, 3, 23, 17, 50, 59, 0, time.UTC)
	assert.Equal(t, api.Time(expected), r.Deadline)
	assert.Equal(t, api.Duration(1*time.Hour+35*time.Minute), r.Extension)

	result, err := json.Marshal(r)
	assert.NilError(t, err)

	assert.Equal(t, orig, string(result))
}

func TestTrimWhitespace(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	userID := uid.New()
	// nolint:noctx
	req, err := http.NewRequest(http.MethodPost, "/api/grants", jsonBody(t, api.CreateGrantRequest{
		User:      userID,
		Privilege: "admin   ",
		Resource:  " kubernetes.production.*",
	}))
	assert.NilError(t, err)
	req.Header.Add("Authorization", "Bearer "+adminAccessKey(srv))
	req.Header.Add("Infra-Version", "0.13.1")

	resp := httptest.NewRecorder()
	routes.ServeHTTP(resp, req)
	assert.Equal(t, resp.Code, http.StatusCreated, resp.Body.String())

	// nolint:noctx
	req, err = http.NewRequest(http.MethodGet, "/api/grants?privilege=%20admin%20&user_id="+userID.String(), nil)
	assert.NilError(t, err)
	req.Header.Add("Authorization", "Bearer "+adminAccessKey(srv))
	req.Header.Add("Infra-Version", "0.13.1")

	resp = httptest.NewRecorder()
	routes.ServeHTTP(resp, req)
	assert.Equal(t, resp.Code, http.StatusOK)

	rb := &api.ListResponse[api.Grant]{}
	err = json.Unmarshal(resp.Body.Bytes(), rb)
	assert.NilError(t, err)

	assert.Equal(t, len(rb.Items), 2, rb.Items)
	expected := api.Grant{
		User:      userID,
		Privilege: "admin",
		Resource:  "kubernetes.production.*",
	}
	assert.DeepEqual(t, rb.Items[1], expected, cmpAPIGrantShallow)
}

func TestWrapRoute_TxnRollbackOnError(t *testing.T) {
	srv := newServer(Options{})
	srv.db = setupDB(t)

	router := gin.New()

	r := route[api.EmptyRequest, *api.EmptyResponse]{
		handler: func(c *gin.Context, request *api.EmptyRequest) (*api.EmptyResponse, error) {
			rCtx := getRequestContext(c)

			user := &models.Identity{
				Model:              models.Model{ID: 1555},
				Name:               "user@example.com",
				OrganizationMember: models.OrganizationMember{OrganizationID: srv.db.DefaultOrg.ID},
			}
			if err := data.CreateIdentity(rCtx.DBTxn, user); err != nil {
				return nil, err
			}

			return nil, fmt.Errorf("this failed")
		},
		infraVersionHeaderOptional: true,
		noAuthentication:           true,
		noOrgRequired:              true,
	}

	api := &API{server: srv}
	add(api, rg(router.Group("/")), "POST", "/do", r)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/do", nil)
	router.ServeHTTP(resp, req)

	assert.Equal(t, resp.Code, http.StatusInternalServerError)

	// The user should not exist, because the txn was rollbed back
	_, err := data.GetIdentity(srv.db, data.ByID(uid.ID(1555)))
	assert.ErrorIs(t, err, internal.ErrNotFound)
}

func TestWrapRoute_HandleErrorOnCommit(t *testing.T) {
	srv := newServer(Options{})
	srv.db = setupDB(t)

	router := gin.New()

	r := route[api.EmptyRequest, *api.EmptyResponse]{
		handler: func(c *gin.Context, request *api.EmptyRequest) (*api.EmptyResponse, error) {
			rCtx := getRequestContext(c)

			// Commit the transaction so that the call in wrapRoute returns an error
			err := rCtx.DBTxn.Commit()
			return nil, err
		},
		infraVersionHeaderOptional: true,
		noAuthentication:           true,
		noOrgRequired:              true,
	}

	api := &API{server: srv}
	add(api, rg(router.Group("/")), "POST", "/do", r)

	resp := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/do", nil)
	router.ServeHTTP(resp, req)

	assert.Equal(t, resp.Code, http.StatusInternalServerError)
}

func TestInfraVersionHeader(t *testing.T) {
	srv := setupServer(t, withAdminUser)
	routes := srv.GenerateRoutes()

	body := jsonBody(t, api.CreateUserRequest{Name: "usera@example.com"})
	// nolint:noctx
	req, err := http.NewRequest(http.MethodPost, "/api/users", body)
	assert.NilError(t, err)
	req.Header.Set("Authorization", "Bearer "+adminAccessKey(srv))

	resp := httptest.NewRecorder()
	routes.ServeHTTP(resp, req)

	assert.Equal(t, resp.Code, http.StatusBadRequest, resp.Body.String())

	respBody := &api.Error{}
	err = json.Unmarshal(resp.Body.Bytes(), respBody)
	assert.NilError(t, err)

	assert.Assert(t, strings.Contains(respBody.Message, "Infra-Version header is required"), respBody.Message)
}

var apiVersionLatest = internal.FullVersion()
