package instances

import (
	"github.com/abccloud/abccloud-go-sdk/client"
	"github.com/abccloud/config"
)

const (
	STATUS_CODE_CREATE_SUCCESS                  = 201
	STATUS_CODE_DELETE_SUCCESS                  = 204
	STATUS_CODE_CREATE_FAILURE_NAME_DUPLICATION = 409
	STATUS_CODE_FAILURE                         = 500
	STATUS_CODE_SUCCESS                         = 200
	STATUS_CODE_NOT_FOUND                       = 404
	STATUS_CODE_PROHIBITED_TO_USE               = 403
)

type VMService struct {
	client.Client
}

type CreateRequest struct {
	Name string
}

type DeleteRequest struct {
	Uuid string
}

type DeleteResponse struct {
	StatusCode int
}

type CreateResponse struct {
	StatusCode int
	Uuid       string
}

type GetResponse struct {
	StatusCode int
	Uuid       string
	Name       string
}

type Instance struct {
	Uuid string
	Name string
}

type ListResponse struct {
	StatusCode int
	Instances  []Instance
}

type GetStatusResponse struct {
	StatusCode     int
	CPUUtilization uint32
}

// New creates a new instance of the VMService client
func New(cfg *config.Config) *VMService {
	// TODO: Logic for creating client using cloud credentials
	return &VMService{Client: client.New(cfg)}
}

func (s *VMService) Create(req CreateRequest) CreateResponse {
	// TODO: Implementation
}

func (s *VMService) List() ListResponse {
	// TODO: Implementation
}

func (s *VMService) Get(uuid string) *GetResponse {
	// TODO: Implementation
	return &GetResponse{}
}

func (s *VMService) GetStatus(uid string) GetStatusResponse {
	// TODO: Implementation
}

func (s *VMService) Delete(req DeleteRequest) DeleteResponse {
	// TODO: Implementation
}

func (s *VMService) NameAvailable(name string) bool {
	// TODO: Implementation
	return true
}
