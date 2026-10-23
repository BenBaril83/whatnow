package host

import (
	"context"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Provider is the identity provider behind sign-in.
type Provider interface {
	// AuthURL is where to send the browser, carrying state back to the callback and nonce into the ID token.
	AuthURL(state, nonce string) string
	// Exchange trades the callback's code for a verified identity whose ID token carries nonce.
	Exchange(ctx context.Context, code, nonce string) (Identity, error)
}

// Google is sign-in with Google over OpenID Connect.
type Google struct {
	config   oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// NewGoogle fetches Google's OpenID configuration. redirectURL is the public callback address.
func NewGoogle(ctx context.Context, clientID, clientSecret, redirectURL string) (*Google, error) {
	p, err := oidc.NewProvider(ctx, "https://accounts.google.com")
	if err != nil {
		return nil, fmt.Errorf("google openid configuration: %w", err)
	}
	return &Google{
		config: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     p.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		verifier: p.Verifier(&oidc.Config{ClientID: clientID}),
	}, nil
}

func (g *Google) AuthURL(state, nonce string) string {
	return g.config.AuthCodeURL(state, oidc.Nonce(nonce))
}

func (g *Google) Exchange(ctx context.Context, code, nonce string) (Identity, error) {
	tok, err := g.config.Exchange(ctx, code)
	if err != nil {
		return Identity{}, err
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		return Identity{}, errors.New("google: no id_token in token response")
	}
	idt, err := g.verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, err
	}
	if idt.Nonce != nonce {
		return Identity{}, errors.New("google: id_token nonce mismatch")
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := idt.Claims(&claims); err != nil {
		return Identity{}, err
	}
	if !claims.EmailVerified {
		return Identity{}, errors.New("google: email not verified")
	}
	return Identity{Subject: idt.Subject, Email: claims.Email, Name: claims.Name}, nil
}
