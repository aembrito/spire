package bundle

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/spire/pkg/common/telemetry"
	"github.com/spiffe/spire/pkg/server/api"
	"github.com/spiffe/spire/pkg/server/api/rpccontext"
	"github.com/spiffe/spire/pkg/server/plugin/datastore"
	"github.com/spiffe/spire/proto/spire-next/api/server/bundle/v1"
	"github.com/spiffe/spire/proto/spire-next/types"
	"github.com/spiffe/spire/proto/spire/common"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var defaultMask = &types.BundleMask{
	TrustDomain:     true,
	RefreshHint:     true,
	SequenceNumber:  true,
	X509Authorities: true,
	JwtAuthorities:  true,
}

// RegisterService registers the bundle service on the gRPC server.
func RegisterService(s *grpc.Server, service *Service) {
	bundle.RegisterBundleServer(s, service)
}

// Config is the service configuration
type Config struct {
	Datastore   datastore.DataStore
	TrustDomain spiffeid.TrustDomain
}

// New creates a new bundle service
func New(config Config) *Service {
	return &Service{
		ds: config.Datastore,
		td: config.TrustDomain,
	}
}

// Service implements the v1 bundle service
type Service struct {
	ds datastore.DataStore
	td spiffeid.TrustDomain
}

func (s *Service) GetBundle(ctx context.Context, req *bundle.GetBundleRequest) (*types.Bundle, error) {
	log := rpccontext.Logger(ctx)

	dsResp, err := s.ds.FetchBundle(ctx, &datastore.FetchBundleRequest{
		TrustDomainId: s.td.IDString(),
	})
	if err != nil {
		log.Errorf("Failed to fetch bundle: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to fetch bundle: %v", err)
	}

	if dsResp.Bundle == nil {
		log.Error("Bundle not found")
		return nil, status.Error(codes.NotFound, "bundle not found")
	}

	b, err := applyMask(dsResp.Bundle, req.OutputMask)
	if err != nil {
		log.Errorf("Failed to apply mask: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to apply mask: %v", err)
	}

	return b, nil
}

func (s *Service) AppendBundle(ctx context.Context, req *bundle.AppendBundleRequest) (*types.Bundle, error) {
	return nil, status.Errorf(codes.Unimplemented, "method AppendBundle not implemented")
}

func (s *Service) PublishJWTAuthority(ctx context.Context, req *bundle.PublishJWTAuthorityRequest) (*bundle.PublishJWTAuthorityResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method PublishJWTAuthority not implemented")
}

func (s *Service) ListFederatedBundles(ctx context.Context, req *bundle.ListFederatedBundlesRequest) (*bundle.ListFederatedBundlesResponse, error) {
	log := rpccontext.Logger(ctx)

	listReq := &datastore.ListBundlesRequest{}

	// Set pagination parameters
	if req.PageSize > 0 {
		listReq.Pagination = &datastore.Pagination{
			PageSize: req.PageSize,
			Token:    req.PageToken,
		}
	}

	dsResp, err := s.ds.ListBundles(ctx, listReq)
	if err != nil {
		log.WithError(err).Error("Failed to list bundles")
		return nil, status.Errorf(codes.Internal, "failed to list bundles: %v", err)
	}

	resp := &bundle.ListFederatedBundlesResponse{}

	if dsResp.Pagination != nil {
		resp.NextPageToken = dsResp.Pagination.Token
	}

	for _, dsBundle := range dsResp.Bundles {
		td, err := spiffeid.TrustDomainFromString(dsBundle.TrustDomainId)
		if err != nil {
			log.WithFields(logrus.Fields{
				logrus.ErrorKey:         err,
				telemetry.TrustDomainID: dsBundle.TrustDomainId,
			}).Errorf("Bundle has an invalid trust domain ID")
			return nil, status.Errorf(codes.Internal, "bundle has an invalid trust domain ID: %q", dsBundle.TrustDomainId)
		}

		// Filter server bundle
		if s.td.Compare(td) == 0 {
			continue
		}

		b, err := applyMask(dsBundle, req.OutputMask)
		if err != nil {
			log.WithError(err).Error("Failed to apply mask")
			return nil, status.Errorf(codes.Internal, "failed to apply mask: %v", err)
		}

		resp.Bundles = append(resp.Bundles, b)
	}

	return resp, nil
}

func (s *Service) GetFederatedBundle(ctx context.Context, req *bundle.GetFederatedBundleRequest) (*types.Bundle, error) {
	log := rpccontext.Logger(ctx)

	td, err := spiffeid.TrustDomainFromString(req.TrustDomain)
	if err != nil {
		log.Errorf("Trust domain argument is not a valid SPIFFE ID: %q", req.TrustDomain)
		return nil, status.Errorf(codes.InvalidArgument, "trust domain argument is not a valid SPIFFE ID: %q", req.TrustDomain)
	}

	if s.td.Compare(td) == 0 {
		log.Errorf("%q is this server own trust domain, use GetBundle RPC instead", td.String())
		return nil, status.Errorf(codes.InvalidArgument, "%q is this server own trust domain, use GetBundle RPC instead", td.String())
	}

	dsResp, err := s.ds.FetchBundle(ctx, &datastore.FetchBundleRequest{
		TrustDomainId: td.IDString(),
	})
	if err != nil {
		log.Errorf("Failed to fetch bundle: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to fetch bundle: %v", err)
	}

	if dsResp.Bundle == nil {
		log.Errorf("Bundle for %q not found", req.TrustDomain)
		return nil, status.Errorf(codes.NotFound, "bundle for %q not found", req.TrustDomain)
	}

	b, err := applyMask(dsResp.Bundle, req.OutputMask)
	if err != nil {
		log.Errorf("Failed to apply mask: %v", err)
		return nil, status.Errorf(codes.Internal, "failed to apply mask: %v", err)
	}

	return b, nil
}

func (s *Service) BatchCreateFederatedBundle(ctx context.Context, req *bundle.BatchCreateFederatedBundleRequest) (*bundle.BatchCreateFederatedBundleResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method BatchCreateFederatedBundle not implemented")
}

func (s *Service) BatchUpdateFederatedBundle(ctx context.Context, req *bundle.BatchUpdateFederatedBundleRequest) (*bundle.BatchUpdateFederatedBundleResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method BatchUpdateFederatedBundle not implemented")
}

func (s *Service) BatchSetFederatedBundle(ctx context.Context, req *bundle.BatchSetFederatedBundleRequest) (*bundle.BatchSetFederatedBundleResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method BatchSetFederatedBundle not implemented")
}

func (s *Service) BatchDeleteFederatedBundle(ctx context.Context, req *bundle.BatchDeleteFederatedBundleRequest) (*bundle.BatchDeleteFederatedBundleResponse, error) {
	log := rpccontext.Logger(ctx)

	if len(req.TrustDomains) == 0 {
		log.Error("Request missing trust domains")
		return nil, status.Error(codes.InvalidArgument, "request missing trust domains")
	}

	var results []*bundle.BatchDeleteFederatedBundleResponse_Result
	for _, trustDomain := range req.TrustDomains {
		err := s.deleteFederatedBundle(ctx, trustDomain)
		results = append(results, &bundle.BatchDeleteFederatedBundleResponse_Result{
			Status:      api.StatusFromError(err),
			TrustDomain: trustDomain,
		})
	}

	return &bundle.BatchDeleteFederatedBundleResponse{
		Results: results,
	}, nil
}

func (s *Service) deleteFederatedBundle(ctx context.Context, trustDomain string) error {
	log := rpccontext.Logger(ctx).WithField(telemetry.TrustDomainID, trustDomain)

	td, err := spiffeid.TrustDomainFromString(trustDomain)
	if err != nil {
		log.WithError(err).Error("Invalid request: malformed trust domain")
		return status.Errorf(codes.InvalidArgument, "malformed trust domain: %v", err)
	}

	if s.td.Compare(td) == 0 {
		log.Error("Invalid request: no possible to delete server bundle")
		return status.Error(codes.InvalidArgument, "no possible to delete server bundle")
	}

	_, err = s.ds.DeleteBundle(ctx, &datastore.DeleteBundleRequest{
		TrustDomainId: td.String(),
		// TODO: what mode must we use here?
		Mode: datastore.DeleteBundleRequest_RESTRICT,
	})
	if err != nil {
		log.WithError(err).Error("Failed to delete Federated Bundle")
		return status.Errorf(codes.Internal, "failed to delete Federated Bundle: %v", err)
	}

	return nil
}

func applyMask(b *common.Bundle, mask *types.BundleMask) (*types.Bundle, error) {
	if mask == nil {
		mask = defaultMask
	}

	out := &types.Bundle{}
	if mask.TrustDomain {
		td, err := spiffeid.TrustDomainFromString(b.TrustDomainId)
		if err != nil {
			return nil, err
		}
		out.TrustDomain = td.String()
	}

	if mask.RefreshHint {
		out.RefreshHint = b.RefreshHint
	}

	if mask.SequenceNumber {
		out.SequenceNumber = 0
	}

	if mask.X509Authorities {
		var authorities []*types.X509Certificate
		for _, rootCA := range b.RootCas {
			authorities = append(authorities, &types.X509Certificate{
				Asn1: rootCA.DerBytes,
			})
		}
		out.X509Authorities = authorities
	}

	if mask.JwtAuthorities {
		var authorities []*types.JWTKey
		for _, JWTSigningKey := range b.JwtSigningKeys {
			authorities = append(authorities, &types.JWTKey{
				PublicKey: JWTSigningKey.PkixBytes,
				KeyId:     JWTSigningKey.Kid,
				ExpiresAt: JWTSigningKey.NotAfter,
			})
		}
		out.JwtAuthorities = authorities
	}

	return out, nil
}
