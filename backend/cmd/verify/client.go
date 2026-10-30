package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type client struct {
	base string
	http *http.Client
}

type response struct {
	Status int
	Body   []byte
	Header http.Header
}

func (r response) code() string {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.Body, &body)
	return body.Error.Code
}

func (r response) into(target any) error {
	if err := json.Unmarshal(r.Body, target); err != nil {
		return fmt.Errorf("yanıt çözülemedi (%d): %s", r.Status, truncate(r.Body))
	}
	return nil
}

func truncate(body []byte) string {
	if len(body) > 200 {
		return string(body[:200])
	}
	return string(body)
}

func (c *client) do(method, path, token string, payload any, headers map[string]string) (response, error) {
	var reader io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return response{}, err
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		return response{}, err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	result, err := c.http.Do(request)
	if err != nil {
		return response{}, err
	}
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		return response{}, err
	}
	return response{Status: result.StatusCode, Body: body, Header: result.Header}, nil
}

func (c *client) get(path, token string) (response, error) {
	return c.do(http.MethodGet, path, token, nil, nil)
}

func (c *client) post(path, token string, payload any) (response, error) {
	return c.do(http.MethodPost, path, token, payload, nil)
}

func (c *client) expect(want int, method, path, token string, payload any, target any) error {
	result, err := c.do(method, path, token, payload, nil)
	if err != nil {
		return err
	}
	if result.Status != want {
		return fmt.Errorf("%s %s: beklenen %d, gelen %d: %s", method, path, want, result.Status, truncate(result.Body))
	}
	if target != nil {
		return result.into(target)
	}
	return nil
}

func newClient(base string) *client {
	return &client{base: base, http: &http.Client{
		Timeout:   60 * time.Second,
		Transport: &http.Transport{MaxIdleConns: 400, MaxIdleConnsPerHost: 400, MaxConnsPerHost: 400},
	}}
}
