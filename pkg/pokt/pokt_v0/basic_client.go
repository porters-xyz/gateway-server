package pokt_v0

import (
	"errors"
	"github.com/pquerna/ffjson/ffjson"
	"github.com/valyala/fasthttp"
	"math/rand"
	"pokt_gateway_server/pkg/common"
	"pokt_gateway_server/pkg/pokt/pokt_v0/models"
	"time"
)

const (
	endpointClientPrefix = "/v1/client"
	endpointQueryPrefix  = "/v1/query"
	endpointDispatch     = endpointClientPrefix + "/dispatch"
	endpointSendRelay    = endpointClientPrefix + "/relay"
	endpointGetHeight    = endpointQueryPrefix + "/height"
	endpointGetApps      = endpointQueryPrefix + "/apps"
	maxApplications      = 5000
)

// BasicClient represents a basic client with a logging, full node host, and a global request timeout.
type BasicClient struct {
	fullNodeHost         string
	globalRequestTimeout time.Duration
}

// NewBasicClient creates a new BasicClient instance.
// Parameters:
//   - fullNodeHost: Full node host address.
//   - logging: Logger instance.
//   - timeout: Global request timeout duration.
//
// Returns:
//   - (*BasicClient): New BasicClient instance.
//   - (error): Error, if any.
func NewBasicClient(fullNodeHost string, timeout time.Duration) (*BasicClient, error) {
	if len(fullNodeHost) == 0 {
		return nil, models.ErrMissingFullNodes
	}
	return &BasicClient{
		fullNodeHost:         fullNodeHost,
		globalRequestTimeout: timeout,
	}, nil
}

// GetSession obtains a session from the full node.
// Parameters:
//   - req: GetSessionRequest instance containing the request parameters.
//
// Returns:
//   - (*GetSessionResponse): Session response.
//   - (error): Error, if any.
func (r BasicClient) GetSession(req *models.GetSessionRequest) (*models.GetSessionResponse, error) {
	var sessionResponse models.GetSessionResponse
	err := r.makeRequest(endpointDispatch, "POST", req, &sessionResponse, nil)
	if err != nil {
		return nil, err
	}
	return &sessionResponse, nil
}

// GetLatestStakedApplications obtains all the applications from the latest block then filters for staked.
// Returns:
//   - ([]*models.PoktApplication): list of staked applications
//   - (error): Error, if any.
func (r BasicClient) GetLatestStakedApplications() ([]*models.PoktApplication, error) {
	reqParams := map[string]any{"opts": map[string]any{"per_page": maxApplications}}
	var resp models.GetApplicationResponse
	err := r.makeRequest(endpointGetApps, "POST", reqParams, &resp, nil)
	if err != nil {
		return nil, err
	}
	stakedApplications := []*models.PoktApplication{}
	for _, app := range resp.Result {
		stakedApplications = append(stakedApplications, app)
	}
	if len(stakedApplications) == 0 {
		return nil, errors.New("zero applications found")
	}
	return stakedApplications, nil
}

// SendRelay sends a relay request to the full node.
// Parameters:
//   - req: SendRelayRequest instance containing the relay request parameters.
//
// Returns:
//   - (*SendRelayResponse): Relay response.
//   - (error): Error, if any.
func (r BasicClient) SendRelay(req *models.SendRelayRequest) (*models.SendRelayResponse, error) {

	// Get a session from the request or retrieve from full node
	session, err := GetSessionFromRequest(r, req)

	if err != nil {
		return nil, err
	}

	// Get the preferred selected node, or chose a random one.
	node, err := getNodeFromRequest(session, req.SelectedNodePubKey)

	if err != nil {
		return nil, err
	}

	currentSessionHeight := session.SessionHeader.SessionHeight

	relayMetadata := &models.RelayMeta{BlockHeight: currentSessionHeight}

	entropy := uint64(rand.Int63())
	relayProof := generateRelayProof(entropy, req.Chain, currentSessionHeight, node.PublicKey, relayMetadata, req.Payload, req.Signer)

	// Relay created, generating a request to the servicer
	var sessionResponse models.SendRelayResponse
	err = r.makeRequest(endpointSendRelay, "POST", &models.Relay{
		Payload:    req.Payload,
		Metadata:   relayMetadata,
		RelayProof: relayProof,
	}, &sessionResponse, &node.ServiceUrl)

	if err != nil {
		return nil, err
	}

	return &sessionResponse, nil
}

// GetLatestBlockHeight gets the latest block height from the full node.
// Returns:
//   - (*GetLatestBlockHeightResponse): Latest block height response.
//   - (error): Error, if any.
func (r BasicClient) GetLatestBlockHeight() (*models.GetLatestBlockHeightResponse, error) {

	var height models.GetLatestBlockHeightResponse
	err := r.makeRequest(endpointGetHeight, "POST", nil, &height, nil)

	if err != nil {
		return nil, err
	}

	return &height, nil
}

func (r BasicClient) makeRequest(endpoint string, method string, requestData any, responseModel any, hostOverride *string) error {
	reqPayload, err := ffjson.Marshal(requestData)
	if err != nil {
		return err
	}

	request := fasthttp.AcquireRequest()
	response := fasthttp.AcquireResponse()

	defer func() {
		fasthttp.ReleaseRequest(request)
		fasthttp.ReleaseResponse(response)
	}()

	if hostOverride != nil {
		request.SetRequestURI(*hostOverride + endpoint)
	} else {
		request.SetRequestURI(r.fullNodeHost + endpoint)
	}
	request.Header.SetMethod(method)

	if method == "POST" {
		request.SetBody(reqPayload)
	}

	err = fasthttp.DoTimeout(request, response, r.globalRequestTimeout)
	if err != nil {
		return err
	}

	// Check for a successful HTTP status code
	if !common.IsHttpOk(response.StatusCode()) {
		var pocketError models.PocketRPCError
		err := ffjson.Unmarshal(response.Body(), pocketError)
		// failed to unmarshal, not sure what the response code is
		if err != nil {
			return models.PocketRPCError{HttpCode: uint64(response.StatusCode()), Message: string(response.Body())}
		}
		return pocketError
	}
	return ffjson.Unmarshal(response.Body(), responseModel)
}
