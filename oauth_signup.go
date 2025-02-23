/*
 * Copyright © 2020-2021 Musing Studio LLC.
 *
 * This file is part of WriteFreely.
 *
 * WriteFreely is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License, included
 * in the LICENSE file in this source code package.
 */

package writefreely

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/writeas/impart"
	"github.com/writeas/web-core/auth"
	"github.com/writefreely/writefreely/page"
	"html/template"
	"net/http"
	"strings"
	"time"
)

type viewOauthSignupVars struct {
	page.StaticPage
	To      string
	Message template.HTML
	Flashes []template.HTML

	AccessToken     string
	TokenUsername   string
	TokenAlias      string // TODO: rename this to match the data it represents: the collection title
	TokenEmail      string
	TokenRemoteUser string
	Provider        string
	ClientID        string
	TokenHash       string
	InviteCode      string

	LoginUsername string
	Alias         string // TODO: rename this to match the data it represents: the collection title
	Email         string
}

const (
	oauthParamAccessToken       = "access_token"
	oauthParamTokenUsername     = "token_username"
	oauthParamTokenAlias        = "token_alias"
	oauthParamTokenEmail        = "token_email"
	oauthParamTokenRemoteUserID = "token_remote_user"
	oauthParamClientID          = "client_id"
	oauthParamProvider          = "provider"
	oauthParamHash              = "signature"
	oauthParamUsername          = "username"
	oauthParamAlias             = "alias"
	oauthParamEmail             = "email"
	oauthParamPassword          = "password"
	oauthParamInviteCode        = "invite_code"
)

type oauthSignupPageParams struct {
	AccessToken     string
	TokenUsername   string
	TokenAlias      string // TODO: rename this to match the data it represents: the collection title
	TokenEmail      string
	TokenRemoteUser string
	ClientID        string
	Provider        string
	TokenHash       string
	InviteCode      string
}

func (p oauthSignupPageParams) HashTokenParams(key string) string {
	hasher := sha256.New()
	hasher.Write([]byte(key))
	hasher.Write([]byte(p.AccessToken))
	hasher.Write([]byte(p.TokenUsername))
	hasher.Write([]byte(p.TokenAlias))
	hasher.Write([]byte(p.TokenEmail))
	hasher.Write([]byte(p.TokenRemoteUser))
	hasher.Write([]byte(p.ClientID))
	hasher.Write([]byte(p.Provider))
	return hex.EncodeToString(hasher.Sum(nil))
}

func (h oauthHandler) viewOauthSignup(app *App, w http.ResponseWriter, r *http.Request) error {
	tp := &oauthSignupPageParams{
		AccessToken:     r.FormValue(oauthParamAccessToken),
		TokenUsername:   r.FormValue(oauthParamTokenUsername),
		TokenAlias:      r.FormValue(oauthParamTokenAlias),
		TokenEmail:      r.FormValue(oauthParamTokenEmail),
		TokenRemoteUser: r.FormValue(oauthParamTokenRemoteUserID),
		ClientID:        r.FormValue(oauthParamClientID),
		Provider:        r.FormValue(oauthParamProvider),
		InviteCode:      r.FormValue(oauthParamInviteCode),
	}
	if tp.HashTokenParams(h.Config.Server.HashSeed) != r.FormValue(oauthParamHash) {
		return impart.HTTPError{Status: http.StatusBadRequest, Message: "Request has been tampered with."}
	}
	tp.TokenHash = tp.HashTokenParams(h.Config.Server.HashSeed)
	if err := h.validateOauthSignup(r); err != nil {
		return h.showOauthSignupPage(app, w, r, tp, err)
	}

	var err error
	hashedPass := []byte{}
	clearPass := r.FormValue(oauthParamPassword)
	hasPass := clearPass != ""
	if hasPass {
		hashedPass, err = auth.HashPass([]byte(clearPass))
		if err != nil {
			return h.showOauthSignupPage(app, w, r, tp, fmt.Errorf("unable to hash password"))
		}
	}
	newUser := &User{
		Username:   r.FormValue(oauthParamUsername),
		HashedPass: hashedPass,
		HasPass:    hasPass,
		Email:      prepareUserEmail(r.FormValue(oauthParamEmail), h.EmailKey),
		Created:    time.Now().Truncate(time.Second).UTC(),
	}
	displayName := r.FormValue(oauthParamAlias)
	if len(displayName) == 0 {
		displayName = r.FormValue(oauthParamUsername)
	}

	err = h.DB.CreateUser(h.Config, newUser, displayName, "")
	if err != nil {
		return h.showOauthSignupPage(app, w, r, tp, err)
	}

	// Log invite if needed
	if tp.InviteCode != "" {
		err = app.db.CreateInvitedUser(tp.InviteCode, newUser.ID)
		if err != nil {
			return err
		}
	}

	err = h.DB.RecordRemoteUserID(r.Context(), newUser.ID, r.FormValue(oauthParamTokenRemoteUserID), r.FormValue(oauthParamProvider), r.FormValue(oauthParamClientID), r.FormValue(oauthParamAccessToken))
	if err != nil {
		return h.showOauthSignupPage(app, w, r, tp, err)
	}

	if err := loginOrFail(h.Store, w, r, newUser); err != nil {
		return h.showOauthSignupPage(app, w, r, tp, err)
	}
	return nil
}

func (h oauthHandler) validateOauthSignup(r *http.Request) error {
	username := r.FormValue(oauthParamUsername)
	if len(username) < h.Config.App.MinUsernameLen {
		return impart.HTTPError{Status: http.StatusBadRequest, Message: "Username is too short."}
	}
	if len(username) > 100 {
		return impart.HTTPError{Status: http.StatusBadRequest, Message: "Username is too long."}
	}
	collTitle := r.FormValue(oauthParamAlias)
	if len(collTitle) == 0 {
		collTitle = username
	}
	email := r.FormValue(oauthParamEmail)
	if len(email) > 0 {
		parts := strings.Split(email, "@")
		if len(parts) != 2 || (len(parts[0]) < 1 || len(parts[1]) < 1) {
			return impart.HTTPError{Status: http.StatusBadRequest, Message: "Invalid email address"}
		}
	}
	return nil
}

func (h oauthHandler) showOauthSignupPage(app *App, w http.ResponseWriter, r *http.Request, tp *oauthSignupPageParams, errMsg error) error {
	var err error
	hashedPass := []byte{}
	hasPass := false
	newUser := &User{
		Username:   strings.Replace(strings.ToLower(tp.TokenUsername),"_","-",-1),
		HashedPass: hashedPass,
		HasPass:    hasPass,
		Email:      prepareUserEmail("", h.EmailKey),
		Created:    time.Now().Truncate(time.Second).UTC(),
	}
	displayName := tp.TokenAlias
	if len(displayName) == 0 {
		displayName = tp.TokenUsername
	}
	displayName = displayName + "'s Blog"

	err = h.DB.CreateUser(h.Config, newUser, displayName, "")
	if err != nil {
		return err
	}

	// Log invite if needed
	if tp.InviteCode != "" {
		err = app.db.CreateInvitedUser(tp.InviteCode, newUser.ID)
		if err != nil {
			return err
		}
	}

	err = h.DB.RecordRemoteUserID(r.Context(), newUser.ID, tp.TokenRemoteUser, tp.Provider, tp.ClientID, tp.AccessToken)
	if err != nil {
		return err
	}

	if err := loginOrFail(h.Store, w, r, newUser); err != nil {
		return err
	}
	return nil
}
