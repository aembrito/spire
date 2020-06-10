package bundle_test

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/spiffe/go-spiffe/spiffetest"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/spire/pkg/common/telemetry"
	"github.com/spiffe/spire/pkg/server/api"
	"github.com/spiffe/spire/pkg/server/api/bundle/v1"
	"github.com/spiffe/spire/pkg/server/api/rpccontext"
	"github.com/spiffe/spire/pkg/server/plugin/datastore"
	bundlepb "github.com/spiffe/spire/proto/spire-next/api/server/bundle/v1"
	"github.com/spiffe/spire/proto/spire-next/types"
	"github.com/spiffe/spire/proto/spire/common"
	"github.com/spiffe/spire/test/fakes/fakedatastore"
	"github.com/spiffe/spire/test/spiretest"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

var (
	ctx      = context.Background()
	td       = spiffeid.RequireTrustDomainFromString("example.org")
	tdBundle = &common.Bundle{
		TrustDomainId: "spiffe://example.org",
		RefreshHint:   60,
		RootCas:       []*common.Certificate{{DerBytes: []byte("cert-bytes-0")}},
		JwtSigningKeys: []*common.PublicKey{
			{
				Kid:       "key-id-0",
				NotAfter:  1590514224,
				PkixBytes: []byte("key-bytes-0"),
			},
		},
	}
	federatedBundle = &common.Bundle{
		TrustDomainId: "spiffe://another-example.org",
		RefreshHint:   60,
		RootCas:       []*common.Certificate{{DerBytes: []byte("cert-bytes-1")}},
		JwtSigningKeys: []*common.PublicKey{
			{
				Kid:       "key-id-1",
				NotAfter:  1590514224,
				PkixBytes: []byte("key-bytes-1"),
			},
		},
	}
)

func TestGetFederatedBundle(t *testing.T) {
	test := setupServiceTest(t)
	defer test.Cleanup()

	for _, tt := range []struct {
		name        string
		trustDomain string
		err         string
		logMsg      string
		outputMask  *types.BundleMask
		isAdmin     bool
		isAgent     bool
		isLocal     bool
		setBundle   bool
	}{
		{
			name:    "Trust domain is empty",
			isAdmin: true,
			err:     `trust domain argument is not a valid SPIFFE ID: ""`,
			logMsg:  `Trust domain argument is not a valid SPIFFE ID: ""`,
		},
		{
			name:        "Trust domain is not a valid trust domain",
			isAdmin:     true,
			trustDomain: "//not-valid",
			err:         `trust domain argument is not a valid SPIFFE ID: "//not-valid"`,
			logMsg:      `Trust domain argument is not a valid SPIFFE ID: "//not-valid"`,
		},
		{
			name:        "The given trust domain is server's own trust domain",
			isAdmin:     true,
			trustDomain: "example.org",
			err:         `"example.org" is this server own trust domain, use GetBundle RPC instead`,
			logMsg:      `"example.org" is this server own trust domain, use GetBundle RPC instead`,
		},
		{
			name:        "Trust domain not found",
			isAdmin:     true,
			trustDomain: "another-example.org",
			err:         `bundle for "another-example.org" not found`,
			logMsg:      `Bundle for "another-example.org" not found`,
		},
		{
			name:        "Get federated bundle do not returns fields filtered by mask",
			isAdmin:     true,
			trustDomain: "another-example.org",
			setBundle:   true,
			outputMask: &types.BundleMask{
				RefreshHint:     false,
				SequenceNumber:  false,
				X509Authorities: false,
				JwtAuthorities:  false,
			},
		},
		{
			name:        "Get federated bundle succeeds for admin workloads",
			isAdmin:     true,
			trustDomain: "another-example.org",
			setBundle:   true,
		},
		{
			name:        "Get federated bundle succeeds for local workloads",
			isLocal:     true,
			trustDomain: "another-example.org",
			setBundle:   true,
		},
		{
			name:        "Get federated bundle succeeds for agent workload",
			isAgent:     true,
			trustDomain: "another-example.org",
			setBundle:   true,
		},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			test.isAdmin = tt.isAdmin
			test.isAgent = tt.isAgent
			test.isLocal = tt.isLocal

			if tt.setBundle {
				test.setBundle(t, federatedBundle)
			}

			b, err := test.client.GetFederatedBundle(context.Background(), &bundlepb.GetFederatedBundleRequest{
				TrustDomain: tt.trustDomain,
				OutputMask:  tt.outputMask,
			})

			if tt.err != "" {
				require.Nil(t, b)
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.err)
				require.Contains(t, test.logHook.LastEntry().Message, tt.logMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, b)
			assertBundleWithMask(t, federatedBundle, b, tt.outputMask)
		})
	}
}

func TestGetBundle(t *testing.T) {
	for _, tt := range []struct {
		name       string
		err        string
		logMsg     string
		outputMask *types.BundleMask
		setBundle  bool
	}{
		{
			name:      "Get bundle returns bundle",
			setBundle: true,
		},
		{
			name:   "Bundle not found",
			err:    `bundle not found`,
			logMsg: `Bundle not found`,
		},
		{
			name:      "Get bundle does not return fields filtered by mask",
			setBundle: true,
			outputMask: &types.BundleMask{
				RefreshHint:     false,
				SequenceNumber:  false,
				X509Authorities: false,
				JwtAuthorities:  false,
			},
		},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			test := setupServiceTest(t)
			defer test.Cleanup()

			if tt.setBundle {
				test.setBundle(t, tdBundle)
			}

			b, err := test.client.GetBundle(context.Background(), &bundlepb.GetBundleRequest{
				OutputMask: tt.outputMask,
			})

			if tt.err != "" {
				require.Nil(t, b)
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.err)
				require.Contains(t, test.logHook.LastEntry().Message, tt.logMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, b)
			assertBundleWithMask(t, tdBundle, b, tt.outputMask)
		})
	}
}

func TestAppendBundle(t *testing.T) {
	ca := spiffetest.NewCA(t)
	rootCA := ca.Roots()[0]

	pkixBytes, err := base64.StdEncoding.DecodeString("MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEYSlUVLqTD8DEnA4F1EWMTf5RXc5lnCxw+5WKJwngEL3rPc9i4Tgzz9riR3I/NiSlkgRO1WsxBusqpC284j9dXA==")
	require.NoError(t, err)

	sb := &common.Bundle{
		TrustDomainId: td.String(),
		RefreshHint:   60,
		RootCas:       []*common.Certificate{{DerBytes: []byte("cert-bytes")}},
		JwtSigningKeys: []*common.PublicKey{
			{
				Kid:       "key-id-1",
				NotAfter:  1590514224,
				PkixBytes: pkixBytes,
			},
		},
	}

	defaultBundle, err := api.BundleToProto(sb)
	require.NoError(t, err)
	expiresAt := time.Now().Add(time.Minute).Unix()
	jwtKey2 := &types.JWTKey{
		PublicKey: pkixBytes,
		KeyId:     "key-id-2",
		ExpiresAt: expiresAt,
	}
	x509Cert := &types.X509Certificate{
		Asn1: rootCA.Raw,
	}
	_, expectedX509Err := x509.ParseCertificates([]byte("malformed"))
	require.Error(t, expectedX509Err)

	_, expectedJWTErr := x509.ParsePKIXPublicKey([]byte("malformed"))
	require.Error(t, expectedJWTErr)

	for _, tt := range []struct {
		name string

		trustDomain  string
		bundle       *types.Bundle
		code         codes.Code
		dsError      error
		err          string
		expectBundle *types.Bundle
		expectLogs   []spiretest.LogEntry
		invalidEntry bool
		noBundle     bool
		inputMask    *types.BundleMask
		outputMask   *types.BundleMask
	}{
		{
			name: "no input or output mask defined",
			bundle: &types.Bundle{
				TrustDomain: td.String(),
				X509Authorities: []*types.X509Certificate{
					x509Cert,
				},
				JwtAuthorities: []*types.JWTKey{jwtKey2},
				// SequenceNumber and refresh hint are ignored.
				SequenceNumber: 10,
				RefreshHint:    20,
			},
			expectBundle: &types.Bundle{
				TrustDomain:     defaultBundle.TrustDomain,
				RefreshHint:     defaultBundle.RefreshHint,
				JwtAuthorities:  append(defaultBundle.JwtAuthorities, jwtKey2),
				SequenceNumber:  defaultBundle.SequenceNumber,
				X509Authorities: append(defaultBundle.X509Authorities, x509Cert),
			},
		},
		{
			name: "output mask defined",
			bundle: &types.Bundle{
				TrustDomain:     td.String(),
				X509Authorities: []*types.X509Certificate{x509Cert},
				JwtAuthorities:  []*types.JWTKey{jwtKey2},
			},
			expectBundle: &types.Bundle{
				TrustDomain:     defaultBundle.TrustDomain,
				X509Authorities: append(defaultBundle.X509Authorities, x509Cert),
			},
			outputMask: &types.BundleMask{
				X509Authorities: true,
			},
		},
		{
			name: "inputMask defined",
			bundle: &types.Bundle{
				TrustDomain:     td.String(),
				X509Authorities: []*types.X509Certificate{x509Cert},
				JwtAuthorities:  []*types.JWTKey{jwtKey2},
			},
			expectBundle: &types.Bundle{
				TrustDomain:     defaultBundle.TrustDomain,
				RefreshHint:     defaultBundle.RefreshHint,
				JwtAuthorities:  defaultBundle.JwtAuthorities,
				SequenceNumber:  defaultBundle.SequenceNumber,
				X509Authorities: append(defaultBundle.X509Authorities, x509Cert),
			},
			inputMask: &types.BundleMask{
				X509Authorities: true,
			},
		},
		{
			name: "input mask all false",
			bundle: &types.Bundle{
				TrustDomain:     td.String(),
				X509Authorities: []*types.X509Certificate{x509Cert},
				JwtAuthorities:  []*types.JWTKey{jwtKey2},
			},
			expectBundle: defaultBundle,
			inputMask: &types.BundleMask{
				X509Authorities: false,
				JwtAuthorities:  false,
				RefreshHint:     false,
				SequenceNumber:  false,
			},
		},
		{
			name: "output mask all false",
			bundle: &types.Bundle{
				TrustDomain:     td.String(),
				X509Authorities: []*types.X509Certificate{x509Cert},
				JwtAuthorities:  []*types.JWTKey{jwtKey2},
			},
			expectBundle: &types.Bundle{TrustDomain: td.String()},
			outputMask: &types.BundleMask{
				X509Authorities: false,
				JwtAuthorities:  false,
				RefreshHint:     false,
				SequenceNumber:  false,
			},
		},
		{
			name: "no bundle",
			code: codes.InvalidArgument,
			err:  "missing bundle",
			expectLogs: []spiretest.LogEntry{
				{
					Level:   logrus.ErrorLevel,
					Message: "Invalid request: missing bundle",
				},
			},
		},
		{
			name: "malformed trust domain",
			bundle: &types.Bundle{
				TrustDomain: "malformed id",
			},
			code: codes.InvalidArgument,
			err:  `trust domain argument is not a valid SPIFFE ID: "malformed id"`,
			expectLogs: []spiretest.LogEntry{
				{
					Level:   logrus.ErrorLevel,
					Message: `Invalid request: trust domain argument is not a valid SPIFFE ID: "malformed id"`,
					Data: logrus.Fields{
						logrus.ErrorKey: `spiffeid: unable to parse: parse spiffe://malformed id: invalid character " " in host name`,
					},
				},
			},
		},
		{
			name: "no allowed trust domain",
			bundle: &types.Bundle{
				TrustDomain: spiffeid.RequireTrustDomainFromString("another.org").String(),
			},
			code: codes.InvalidArgument,
			err:  "only the trust domain of the server can be appended",
			expectLogs: []spiretest.LogEntry{
				{
					Level:   logrus.ErrorLevel,
					Message: "Invalid request: only the trust domain of the server can be appended",
				},
			},
		},
		{
			name: "malformed X509 authority",
			bundle: &types.Bundle{
				TrustDomain: td.String(),
				X509Authorities: []*types.X509Certificate{
					{
						Asn1: []byte("malformed"),
					},
				},
			},
			code: codes.InvalidArgument,
			err:  `invalid X509 authority: asn1:`,
			expectLogs: []spiretest.LogEntry{
				{
					Level:   logrus.ErrorLevel,
					Message: "Invalid request: invalid X509 authority",
					Data: logrus.Fields{
						logrus.ErrorKey: expectedX509Err.Error(),
					},
				},
			},
		},
		{
			name: "malformed JWT authority",
			bundle: &types.Bundle{
				TrustDomain: td.String(),
				JwtAuthorities: []*types.JWTKey{
					{
						PublicKey: []byte("malformed"),
						ExpiresAt: expiresAt,
						KeyId:     "kid2",
					},
				},
			},
			code: codes.InvalidArgument,
			err:  "invalid JWT authority: asn1:",
			expectLogs: []spiretest.LogEntry{
				{
					Level:   logrus.ErrorLevel,
					Message: "Invalid request: invalid JWT authority",
					Data: logrus.Fields{
						logrus.ErrorKey: expectedJWTErr.Error(),
					},
				},
			},
		},
		{
			name: "invalid keyID jwt authority",
			bundle: &types.Bundle{
				TrustDomain: td.String(),
				JwtAuthorities: []*types.JWTKey{
					{
						PublicKey: jwtKey2.PublicKey,
						KeyId:     "",
					},
				},
			},
			code: codes.InvalidArgument,
			err:  "invalid JWT authority: missing KeyId",
			expectLogs: []spiretest.LogEntry{
				{
					Level:   logrus.ErrorLevel,
					Message: "Invalid request: invalid JWT authority",
					Data: logrus.Fields{
						logrus.ErrorKey: "missing KeyId",
					},
				},
			},
		},
		{
			name: "datasource fails",
			bundle: &types.Bundle{
				TrustDomain:     td.String(),
				X509Authorities: []*types.X509Certificate{x509Cert},
			},
			code:    codes.Internal,
			dsError: errors.New("some error"),
			err:     "failed to fetch server bundle: some error",
			expectLogs: []spiretest.LogEntry{
				{
					Level:   logrus.ErrorLevel,
					Message: "Failed to fetch server bundle",
					Data: logrus.Fields{
						logrus.ErrorKey: "some error",
					},
				},
			},
		},
		{
			name: "server bundle not found",
			bundle: &types.Bundle{
				TrustDomain:     td.String(),
				X509Authorities: []*types.X509Certificate{x509Cert},
			},
			code: codes.NotFound,
			err:  "failed to fetch server bundle: not found",
			expectLogs: []spiretest.LogEntry{
				{
					Level:   logrus.ErrorLevel,
					Message: "Failed to fetch server bundle: not found",
				},
			},
			noBundle: true,
		},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			test := setupServiceTest(t)
			defer test.Cleanup()

			if !tt.noBundle {
				test.setBundle(t, sb)
			}
			test.ds.SetError(tt.dsError)

			if tt.invalidEntry {
				_, err := test.ds.AppendBundle(ctx, &datastore.AppendBundleRequest{
					Bundle: &common.Bundle{
						TrustDomainId: "malformed",
					},
				})
				require.NoError(t, err)
			}
			resp, err := test.client.AppendBundle(context.Background(), &bundlepb.AppendBundleRequest{
				Bundle:     tt.bundle,
				InputMask:  tt.inputMask,
				OutputMask: tt.outputMask,
			})

			spiretest.AssertLogs(t, test.logHook.AllEntries(), tt.expectLogs)
			if tt.err != "" {
				spiretest.RequireGRPCStatusContains(t, err, tt.code, tt.err)
				require.Nil(t, resp)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, resp)

			spiretest.AssertProtoEqual(t, tt.expectBundle, resp)
		})
	}
}

func TestBatchDeleteFederatedBundle(t *testing.T) {
	test := setupServiceTest(t)
	defer test.Cleanup()

	td1 := spiffeid.RequireTrustDomainFromString("td1.org")
	td2 := spiffeid.RequireTrustDomainFromString("td2.org")
	td3 := spiffeid.RequireTrustDomainFromString("td3.org")
	dsBundles := []string{
		td.String(),
		td1.String(),
		td2.String(),
		td3.String(),
	}

	for _, tt := range []struct {
		name string

		code            codes.Code
		dsError         error
		err             string
		expectLogs      []spiretest.LogEntry
		expectResults   []*bundlepb.BatchDeleteFederatedBundleResponse_Result
		expectDSBundles []string
		trustDomains    []string
	}{
		{
			name: "remove multiple bundles",
			expectResults: []*bundlepb.BatchDeleteFederatedBundleResponse_Result{
				{Status: &types.Status{Code: int32(codes.OK), Message: "OK"}, TrustDomain: td1.String()},
				{Status: &types.Status{Code: int32(codes.OK), Message: "OK"}, TrustDomain: td2.String()},
			},
			expectDSBundles: []string{td.String(), td3.String()},
			trustDomains:    []string{td1.String(), td2.String()},
		},
		{
			name:            "empty trust domains",
			expectResults:   []*bundlepb.BatchDeleteFederatedBundleResponse_Result{},
			expectDSBundles: dsBundles,
		},
		{
			name: "malformed trust domain",
			expectLogs: []spiretest.LogEntry{
				{
					Level:   logrus.ErrorLevel,
					Message: "Invalid request: malformed trust domain",
					Data: logrus.Fields{
						logrus.ErrorKey:         `spiffeid: unable to parse: parse spiffe://malformed TD: invalid character " " in host name`,
						telemetry.TrustDomainID: "malformed TD",
					},
				},
			},
			expectResults: []*bundlepb.BatchDeleteFederatedBundleResponse_Result{
				{
					Status: &types.Status{
						Code:    int32(codes.InvalidArgument),
						Message: `malformed trust domain: spiffeid: unable to parse: parse spiffe://malformed TD: invalid character " " in host name`,
					},
					TrustDomain: "malformed TD",
				},
			},
			expectDSBundles: dsBundles,
			trustDomains:    []string{"malformed TD"},
		},
		{
			name: "fail on server bundle",
			expectLogs: []spiretest.LogEntry{
				{
					Level:   logrus.ErrorLevel,
					Message: "Invalid request: removing the bundle for the server trust domain is not allowed",
					Data: logrus.Fields{
						telemetry.TrustDomainID: td.String(),
					},
				},
			},
			expectResults: []*bundlepb.BatchDeleteFederatedBundleResponse_Result{
				{
					Status: &types.Status{
						Code:    int32(codes.InvalidArgument),
						Message: "removing the bundle for the server trust domain is not allowed",
					},
					TrustDomain: td.String(),
				},
			},
			expectDSBundles: dsBundles,
			trustDomains:    []string{td.String()},
		},
		{
			name: "bundle not found",
			expectResults: []*bundlepb.BatchDeleteFederatedBundleResponse_Result{
				{
					Status: &types.Status{
						Code:    int32(codes.NotFound),
						Message: "no such bundle",
					},
					TrustDomain: "notfound.org",
				},
			},
			expectDSBundles: dsBundles,
			trustDomains:    []string{"notfound.org"},
		},
		{
			name: "failed to delete",
			expectLogs: []spiretest.LogEntry{
				{
					Level:   logrus.ErrorLevel,
					Message: "Failed to delete federated bundle",
					Data: logrus.Fields{
						logrus.ErrorKey:         "datasource fails",
						telemetry.TrustDomainID: td1.String(),
					},
				},
			},
			expectResults: []*bundlepb.BatchDeleteFederatedBundleResponse_Result{
				{
					Status: &types.Status{
						Code:    int32(codes.Internal),
						Message: "failed to delete federated bundle: datasource fails",
					},
					TrustDomain: td1.String(),
				},
			},
			expectDSBundles: dsBundles,
			trustDomains:    []string{td1.String()},
			dsError:         errors.New("datasource fails"),
		},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			test.logHook.Reset()
			test.ds.SetError(tt.dsError)

			// Create all test bundles
			for _, td := range dsBundles {
				_ = createBundle(t, test, td)
			}

			resp, err := test.client.BatchDeleteFederatedBundle(ctx, &bundlepb.BatchDeleteFederatedBundleRequest{
				TrustDomains: tt.trustDomains,
			})

			spiretest.AssertLogs(t, test.logHook.AllEntries(), tt.expectLogs)
			if tt.err != "" {
				spiretest.RequireGRPCStatusContains(t, err, tt.code, tt.err)
				require.Nil(t, resp)

				return
			}

			// Validate response
			require.NoError(t, err)
			require.NotNil(t, resp)
			expectResponse := &bundlepb.BatchDeleteFederatedBundleResponse{
				Results: tt.expectResults,
			}

			spiretest.AssertProtoEqual(t, expectResponse, resp)

			// Validate DS content
			dsResp, err := test.ds.ListBundles(ctx, &datastore.ListBundlesRequest{})
			require.NoError(t, err)

			var dsBundles []string
			for _, b := range dsResp.Bundles {
				dsBundles = append(dsBundles, b.TrustDomainId)
			}
			require.Equal(t, tt.expectDSBundles, dsBundles)
		})
	}
}

func TestListFederatedBundles(t *testing.T) {
	test := setupServiceTest(t)
	defer test.Cleanup()

	_ = createBundle(t, test, td.String())

	td1 := spiffeid.RequireTrustDomainFromString("td1.org")
	b1 := createBundle(t, test, td1.String())

	td2 := spiffeid.RequireTrustDomainFromString("td2.org")
	b2 := createBundle(t, test, td2.String())

	td3 := spiffeid.RequireTrustDomainFromString("td3.org")
	b3 := createBundle(t, test, td3.String())

	for _, tt := range []struct {
		name          string
		code          codes.Code
		err           string
		expectBundles []*common.Bundle
		expectLogs    []spiretest.LogEntry
		expectToken   string
		isInvalidTD   bool
		outputMask    *types.BundleMask
		pageSize      int32
		pageToken     string
	}{
		{
			name:          "no returns fields filtered by mask",
			expectBundles: []*common.Bundle{b1, b2, b3},
			outputMask: &types.BundleMask{
				RefreshHint:     false,
				SequenceNumber:  false,
				X509Authorities: false,
				JwtAuthorities:  false,
			},
		},
		{
			name:          "get only trust domains",
			expectBundles: []*common.Bundle{b1, b2, b3},
			outputMask:    &types.BundleMask{},
		},
		{
			name: "get first page",
			// Returns only one element because server bundle is the first element
			// returned by datastore, and we filter resutls on service
			expectBundles: []*common.Bundle{b1},
			expectToken:   td1.String(),
			pageSize:      2,
		},
		{
			name:          "get second page",
			expectBundles: []*common.Bundle{b2, b3},
			expectToken:   td3.String(),
			pageSize:      2,
			pageToken:     td1.String(),
		},
		{
			name:          "get third page",
			expectBundles: []*common.Bundle{},
			expectToken:   "",
			pageSize:      2,
			pageToken:     td3.String(),
		},
		{
			name: "datastore returns invalid trust domain",
			code: codes.Internal,
			err:  `bundle has an invalid trust domain ID: "invalid TD"`,
			expectLogs: []spiretest.LogEntry{
				{
					Level:   logrus.ErrorLevel,
					Message: "Bundle has an invalid trust domain ID",
					Data: logrus.Fields{
						logrus.ErrorKey:         `spiffeid: unable to parse: parse spiffe://invalid TD: invalid character " " in host name`,
						telemetry.TrustDomainID: "invalid TD",
					},
				},
			},
			isInvalidTD: true,
		},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			test.logHook.Reset()

			// Create an invalid bundle to test mask failing
			if tt.isInvalidTD {
				invalidBundle := createBundle(t, test, "invalid TD")
				defer func() {
					_, _ = test.ds.DeleteBundle(ctx, &datastore.DeleteBundleRequest{
						TrustDomainId: invalidBundle.TrustDomainId,
					})
				}()
			}

			resp, err := test.client.ListFederatedBundles(ctx, &bundlepb.ListFederatedBundlesRequest{
				OutputMask: tt.outputMask,
				PageSize:   tt.pageSize,
				PageToken:  tt.pageToken,
			})

			if tt.err != "" {
				spiretest.RequireGRPCStatusContains(t, err, tt.code, tt.err)
				require.Nil(t, resp)
				spiretest.AssertLogs(t, test.logHook.AllEntries(), tt.expectLogs)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, resp)

			require.Equal(t, tt.expectToken, resp.NextPageToken)
			require.Len(t, resp.Bundles, len(tt.expectBundles))

			for i, b := range resp.Bundles {
				assertBundleWithMask(t, tt.expectBundles[i], b, tt.outputMask)
			}
		})
	}
}

func createBundle(t *testing.T, test *serviceTest, td string) *common.Bundle {
	b := &common.Bundle{
		TrustDomainId: td,
		RefreshHint:   60,
		RootCas:       []*common.Certificate{{DerBytes: []byte(fmt.Sprintf("cert-bytes-%s", td))}},
		JwtSigningKeys: []*common.PublicKey{
			{
				Kid:       fmt.Sprintf("key-id-%s", td),
				NotAfter:  time.Now().Add(time.Minute).Unix(),
				PkixBytes: []byte(fmt.Sprintf("key-bytes-%s", td)),
			},
		},
	}
	test.setBundle(t, b)

	return b
}

func assertBundleWithMask(t *testing.T, expected *common.Bundle, actual *types.Bundle, m *types.BundleMask) {
	require.Equal(t, spiffeid.RequireTrustDomainFromString(expected.TrustDomainId).String(), actual.TrustDomain)

	if m == nil || m.RefreshHint {
		require.Equal(t, expected.RefreshHint, actual.RefreshHint)
	} else {
		require.Zero(t, actual.RefreshHint)
	}

	if m == nil || m.JwtAuthorities {
		require.Equal(t, len(expected.JwtSigningKeys), len(actual.JwtAuthorities))
		require.Equal(t, expected.JwtSigningKeys[0].Kid, actual.JwtAuthorities[0].KeyId)
		require.Equal(t, expected.JwtSigningKeys[0].NotAfter, actual.JwtAuthorities[0].ExpiresAt)
		require.Equal(t, expected.JwtSigningKeys[0].PkixBytes, actual.JwtAuthorities[0].PublicKey)
	} else {
		require.Zero(t, actual.RefreshHint)
	}

	if m == nil || m.X509Authorities {
		require.Equal(t, len(expected.RootCas), len(actual.X509Authorities))
		require.Equal(t, expected.RootCas[0].DerBytes, actual.X509Authorities[0].Asn1)
	} else {
		require.Zero(t, actual.X509Authorities)
	}
}

func (c *serviceTest) setBundle(t *testing.T, b *common.Bundle) {
	req := &datastore.SetBundleRequest{
		Bundle: b,
	}

	_, err := c.ds.SetBundle(context.Background(), req)
	require.NoError(t, err)
}

type serviceTest struct {
	client  bundlepb.BundleClient
	ds      *fakedatastore.DataStore
	logHook *test.Hook
	done    func()
	isAdmin bool
	isAgent bool
	isLocal bool
}

func (c *serviceTest) Cleanup() {
	c.done()
}

func setupServiceTest(t *testing.T) *serviceTest {
	ds := fakedatastore.New()
	service := bundle.New(bundle.Config{
		Datastore:   ds,
		TrustDomain: td,
	})

	log, logHook := test.NewNullLogger()
	registerFn := func(s *grpc.Server) {
		bundle.RegisterService(s, service)
	}

	test := &serviceTest{
		ds:      ds,
		logHook: logHook,
	}

	contextFn := func(ctx context.Context) context.Context {
		ctx = rpccontext.WithLogger(ctx, log)
		if test.isAdmin {
			ctx = rpccontext.WithCallerAdminEntries(ctx, []*types.Entry{{Admin: true}})
		}
		if test.isAgent {
			ctx = rpccontext.WithAgentCaller(ctx)
		}
		if test.isLocal {
			ctx = rpccontext.WithCallerAddr(ctx, &net.UnixAddr{
				Net:  "unix",
				Name: "addr.sock",
			})
		}
		return ctx
	}

	conn, done := spiretest.NewAPIServer(t, registerFn, contextFn)
	test.done = done
	test.client = bundlepb.NewBundleClient(conn)

	return test
}
