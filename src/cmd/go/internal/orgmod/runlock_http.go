// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !cmd_go_bootstrap

package orgmod

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// An httpStore keeps the locks on a buildhost server. The job presents its
// GitHub Actions OIDC token, so no workflow adds a secret. The server keys each
// lock by the repository, run and attempt that the token names.
type httpStore struct {
	base   string
	getenv func(string) string
	client *http.Client
}

func newHTTPStore(u *url.URL, getenv func(string) string) (RunLockStore, error) {
	if getenv("ACTIONS_ID_TOKEN_REQUEST_URL") == "" || getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN") == "" {
		return nil, errors.New("the job has no GitHub Actions OIDC token to present; grant the workflow the permission id-token: write")
	}
	return &httpStore{
		base:   strings.TrimSuffix(u.String(), "/"),
		getenv: getenv,
		client: &http.Client{Timeout: time.Minute},
	}, nil
}

func (s *httpStore) String() string { return s.base }

// runLockBody is the JSON the store reads and answers with.
type runLockBody struct {
	Repository string `json:"repository,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	RunAttempt string `json:"run_attempt,omitempty"`
	Name       string `json:"name,omitempty"`
	Value      string `json:"value,omitempty"`
	Found      bool   `json:"found,omitempty"`
	Created    bool   `json:"created,omitempty"`
}

func (s *httpStore) Lookup(ctx context.Context, key RunLockKey) (string, bool, error) {
	query := url.Values{
		"repository":  {key.Repository},
		"run_id":      {key.ID},
		"run_attempt": {key.Attempt},
		"name":        {key.Name()},
	}
	var answer runLockBody
	if err := s.do(ctx, http.MethodGet, "/api/v1/run-locks?"+query.Encode(), nil, &answer); err != nil {
		return "", false, err
	}
	return answer.Value, answer.Found, nil
}

func (s *httpStore) Claim(ctx context.Context, key RunLockKey, version string) (string, error) {
	body, err := json.Marshal(runLockBody{
		Repository: key.Repository,
		RunID:      key.ID,
		RunAttempt: key.Attempt,
		Name:       key.Name(),
		Value:      version,
	})
	if err != nil {
		return "", err
	}
	var answer runLockBody
	if err := s.do(ctx, http.MethodPost, "/api/v1/run-locks", body, &answer); err != nil {
		return "", err
	}
	return answer.Value, nil
}

// do sends one request with a fresh OIDC token and decodes the answer.
func (s *httpStore) do(ctx context.Context, method, path string, body []byte, answer any) error {
	token, err := s.oidcToken(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, answer); err != nil {
		return fmt.Errorf("%s %s: %v", method, path, err)
	}
	return nil
}

// oidcToken asks GitHub Actions for an OIDC token whose audience is the store.
func (s *httpStore) oidcToken(ctx context.Context) (string, error) {
	tokenURL := s.getenv("ACTIONS_ID_TOKEN_REQUEST_URL")
	sep := "?"
	if strings.Contains(tokenURL, "?") {
		sep = "&"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL+sep+"audience="+url.QueryEscape(s.base), nil)
	if err != nil {
		return "", fmt.Errorf("requesting an OIDC token: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.getenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN"))
	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("requesting an OIDC token: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", fmt.Errorf("requesting an OIDC token: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("requesting an OIDC token: %s", resp.Status)
	}
	var answer struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(data, &answer); err != nil || answer.Value == "" {
		return "", fmt.Errorf("requesting an OIDC token: the answer holds no token")
	}
	return answer.Value, nil
}
