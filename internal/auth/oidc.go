package auth

import (
	"context"
	"errors"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type CoreOSOIDC struct {
	config   oauth2.Config
	verifier *oidc.IDTokenVerifier
	issuer   string
}

func NewCoreOSOIDC(ctx context.Context, issuer, clientID, clientSecret, redirectURL string) (*CoreOSOIDC, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	return &CoreOSOIDC{
		config:   oauth2.Config{ClientID: clientID, ClientSecret: clientSecret, Endpoint: provider.Endpoint(), RedirectURL: redirectURL, Scopes: []string{oidc.ScopeOpenID, "profile", "email"}},
		verifier: provider.Verifier(&oidc.Config{ClientID: clientID}),
		issuer:   issuer,
	}, nil
}

func (c *CoreOSOIDC) AuthorizationURL(state, nonce, challenge string) string {
	return c.config.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.SetAuthURLParam("code_challenge", challenge), oauth2.SetAuthURLParam("code_challenge_method", "S256"))
}

func (c *CoreOSOIDC) Exchange(ctx context.Context, code, verifier, nonce string) (OIDCIdentity, error) {
	token, err := c.config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return OIDCIdentity{}, err
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		return OIDCIdentity{}, errors.New("OIDC 响应缺少 id_token")
	}
	idToken, err := c.verifier.Verify(ctx, raw)
	if err != nil {
		return OIDCIdentity{}, err
	}
	var claims struct {
		Subject string  `json:"sub"`
		Nonce   string  `json:"nonce"`
		Name    string  `json:"name"`
		Email   *string `json:"email"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return OIDCIdentity{}, err
	}
	if claims.Subject == "" || claims.Nonce != nonce {
		return OIDCIdentity{}, errors.New("OIDC subject 或 nonce 不合法")
	}
	name := claims.Name
	if name == "" {
		name = claims.Subject
	}
	return OIDCIdentity{Issuer: c.issuer, Subject: claims.Subject, DisplayName: name, Email: claims.Email}, nil
}
