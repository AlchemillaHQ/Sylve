// SPDX-License-Identifier: BSD-2-Clause
//
// Copyright (c) 2026 The FreeBSD Foundation.
//
// This software was developed by Hayzam Sherif <hayzam@alchemilla.io>
// of Alchemilla Ventures Pvt. Ltd. <hello@alchemilla.io>,
// under sponsorship from the FreeBSD Foundation.

package auth

import (
	"testing"
	"time"

	"github.com/alchemillahq/sylve/internal/db/models"
	clusterModels "github.com/alchemillahq/sylve/internal/db/models/cluster"
	serviceInterfaces "github.com/alchemillahq/sylve/internal/interfaces/services"
	"github.com/alchemillahq/sylve/internal/testutil"

	"github.com/golang-jwt/jwt/v4"
)

func signJWTForParserTest(t *testing.T, method jwt.SigningMethod, claims jwt.Claims, secret string) string {
	t.Helper()

	token, err := jwt.NewWithClaims(method, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	return token
}

func TestJWTParserHardening(t *testing.T) {
	const signingSecret = "signing-secret"

	db := testutil.NewSQLiteTestDB(
		t,
		&models.User{},
		&models.Token{},
		&models.SystemSecrets{},
		&clusterModels.Cluster{},
	)
	if err := db.Create(&models.SystemSecrets{Name: "JWTSecret", Data: signingSecret}).Error; err != nil {
		t.Fatalf("create JWT secret: %v", err)
	}
	if err := db.Create(&clusterModels.Cluster{Enabled: true, Key: signingSecret}).Error; err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	user := models.User{Username: "admin", Admin: true}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	service := &Service{DB: db}
	validators := []struct {
		name         string
		claims       func(*jwt.NumericDate) jwt.Claims
		validate     func(string) (serviceInterfaces.CustomClaims, error)
		persistToken bool
	}{
		{
			name: "local",
			claims: func(expiresAt *jwt.NumericDate) jwt.Claims {
				return JWT{
					RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: expiresAt},
					CustomClaims: serviceInterfaces.CustomClaims{
						UserID:   user.ID,
						Username: user.Username,
						AuthType: "sylve",
					},
				}
			},
			validate:     service.ValidateToken,
			persistToken: true,
		},
		{
			name: "scoped",
			claims: func(expiresAt *jwt.NumericDate) jwt.Claims {
				return ScopedJWT{
					RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: expiresAt},
					Scope:            "sse",
					CustomClaims: serviceInterfaces.CustomClaims{
						UserID:   user.ID,
						Username: user.Username,
						AuthType: "sylve",
					},
				}
			},
			validate: func(token string) (serviceInterfaces.CustomClaims, error) {
				return service.ValidateScopedJWT(token, "sse")
			},
		},
		{
			name: "cluster",
			claims: func(expiresAt *jwt.NumericDate) jwt.Claims {
				var issuedAt *jwt.NumericDate
				if expiresAt != nil {
					issuedAt = jwt.NewNumericDate(expiresAt.Time.Add(-clusterTokenTTL))
				}
				return JWT{
					RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: expiresAt, IssuedAt: issuedAt},
					CustomClaims: serviceInterfaces.CustomClaims{
						UserID:   user.ID,
						Username: user.Username,
						AuthType: "sylve",
						TokenUse: ClusterTokenUseUserProxy,
						Admin:    true,
					},
				}
			},
			validate: service.VerifyClusterJWT,
		},
	}

	now := time.Now()
	testCases := []struct {
		name          string
		signingMethod jwt.SigningMethod
		signingSecret string
		expiresAt     *jwt.NumericDate
		wantError     bool
		localOnly     bool
	}{
		{"valid HS256", jwt.SigningMethodHS256, signingSecret, jwt.NewNumericDate(now.Add(clusterTokenTTL)), false, false},
		{"reject HS384", jwt.SigningMethodHS384, signingSecret, jwt.NewNumericDate(now.Add(clusterTokenTTL)), true, false},
		{"reject missing expiry", jwt.SigningMethodHS256, signingSecret, nil, true, false},
		// Expiry and signature validation are shared library behavior, so one wrapper is sufficient.
		{"reject expired token", jwt.SigningMethodHS256, signingSecret, jwt.NewNumericDate(now.Add(-time.Hour)), true, true},
		{"reject bad signature", jwt.SigningMethodHS256, signingSecret + "-wrong", jwt.NewNumericDate(now.Add(time.Hour)), true, true},
	}

	for _, validator := range validators {
		t.Run(validator.name, func(t *testing.T) {
			for _, test := range testCases {
				if test.localOnly && !validator.persistToken {
					continue
				}

				t.Run(test.name, func(t *testing.T) {
					token := signJWTForParserTest(
						t,
						test.signingMethod,
						validator.claims(test.expiresAt),
						test.signingSecret,
					)
					if validator.persistToken {
						if err := db.Create(&models.Token{
							UserID:   user.ID,
							Token:    token,
							AuthType: "sylve",
							Expiry:   now.Add(time.Hour),
						}).Error; err != nil {
							t.Fatalf("create token record: %v", err)
						}
					}

					claims, err := validator.validate(token)
					if test.wantError {
						if err == nil {
							t.Fatal("expected validation error")
						}
						return
					}
					if err != nil {
						t.Fatalf("unexpected validation error: %v", err)
					}
					if claims.UserID != user.ID {
						t.Fatalf("unexpected user ID: got %d, want %d", claims.UserID, user.ID)
					}
				})
			}
		})
	}
}

func TestClusterJWTClaimBounds(t *testing.T) {
	const signingSecret = "cluster-secret"
	db := testutil.NewSQLiteTestDB(t, &clusterModels.Cluster{})
	if err := db.Create(&clusterModels.Cluster{Enabled: true, Key: signingSecret}).Error; err != nil {
		t.Fatalf("create cluster: %v", err)
	}
	service := &Service{DB: db}
	now := time.Now().Truncate(time.Second)

	tests := []struct {
		name      string
		issuedAt  *jwt.NumericDate
		notBefore *jwt.NumericDate
		expires   *jwt.NumericDate
		tokenUse  string
		wantErr   bool
	}{
		{
			name: "valid five minute token", issuedAt: jwt.NewNumericDate(now),
			expires:  jwt.NewNumericDate(now.Add(clusterTokenTTL)),
			tokenUse: ClusterTokenUseUserProxy,
		},
		{
			name: "missing issued at", expires: jwt.NewNumericDate(now.Add(time.Minute)),
			tokenUse: ClusterTokenUseUserProxy, wantErr: true,
		},
		{
			name: "lifetime over five minutes", issuedAt: jwt.NewNumericDate(now),
			expires:  jwt.NewNumericDate(now.Add(clusterTokenTTL + time.Second)),
			tokenUse: ClusterTokenUseUserProxy, wantErr: true,
		},
		{
			name: "issued within clock skew", issuedAt: jwt.NewNumericDate(now.Add(clusterTokenFutureSkew - time.Second)),
			expires:  jwt.NewNumericDate(now.Add(clusterTokenFutureSkew + time.Minute)),
			tokenUse: ClusterTokenUseInternalControl,
		},
		{
			name: "issued too far in future", issuedAt: jwt.NewNumericDate(now.Add(clusterTokenFutureSkew + time.Second)),
			expires:  jwt.NewNumericDate(now.Add(clusterTokenFutureSkew + time.Minute)),
			tokenUse: ClusterTokenUseInternalControl, wantErr: true,
		},
		{
			name: "not valid yet", issuedAt: jwt.NewNumericDate(now),
			notBefore: jwt.NewNumericDate(now.Add(time.Minute)), expires: jwt.NewNumericDate(now.Add(time.Minute)),
			tokenUse: ClusterTokenUseUserProxy, wantErr: true,
		},
		{
			name: "token use must be exact", issuedAt: jwt.NewNumericDate(now),
			expires: jwt.NewNumericDate(now.Add(time.Minute)), tokenUse: " user_proxy ", wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			token := signJWTForParserTest(t, jwt.SigningMethodHS256, JWT{
				RegisteredClaims: jwt.RegisteredClaims{
					IssuedAt: test.issuedAt, NotBefore: test.notBefore, ExpiresAt: test.expires,
				},
				CustomClaims: serviceInterfaces.CustomClaims{TokenUse: test.tokenUse},
			}, signingSecret)

			_, err := service.VerifyClusterJWT(token)
			if (err != nil) != test.wantErr {
				t.Fatalf("error=%v, wantErr=%v", err, test.wantErr)
			}
		})
	}
}
