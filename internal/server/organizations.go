package server

import (
	"github.com/gin-gonic/gin"

	"github.com/infrahq/infra/api"
	"github.com/infrahq/infra/internal/access"
	"github.com/infrahq/infra/internal/server/models"
)

func (a *API) ListOrganizations(c *gin.Context, r *api.ListOrganizationsRequest) (*api.ListResponse[api.Organization], error) {
	p := PaginationFromRequest(r.PaginationRequest)
	orgs, err := access.ListOrganizations(c, r.Name, &p)
	if err != nil {
		return nil, err
	}

	result := api.NewListResponse(orgs, PaginationToResponse(p), func(org models.Organization) api.Organization {
		return *org.ToAPI()
	})

	return result, nil
}

func (a *API) GetOrganization(c *gin.Context, r *api.Resource) (*api.Organization, error) {
	org, err := access.GetOrganization(c, r.ID)
	if err != nil {
		return nil, err
	}

	return org.ToAPI(), nil
}

func (a *API) CreateOrganization(c *gin.Context, r *api.CreateOrganizationRequest) (*api.Organization, error) {
	org := &models.Organization{
		Name:   r.Name,
		Domain: r.Domain,
	}

	// TODO: This should be removed in the future in favour of setting CreatedBy automatically
	authIdent := getRequestContext(c).Authenticated.User
	if authIdent != nil {
		org.CreatedBy = authIdent.ID
	}

	err := access.CreateOrganization(c, org)
	if err != nil {
		return nil, err
	}

	a.t.Org(org.ID.String(), authIdent.ID.String(), org.Name)

	return org.ToAPI(), nil
}

func (a *API) DeleteOrganization(c *gin.Context, r *api.Resource) (*api.EmptyResponse, error) {
	return nil, access.DeleteOrganization(c, r.ID)
}
