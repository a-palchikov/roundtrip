/*
Copyright 2015 Gravitational, Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
/*
package roundtrip provides convenient functions for building HTTP client wrappers
and providing functions for server responses.

 import (
    "github.com/gravitational/roundtrip"
 )

 type MyClient struct {
     roundtrip.Client // you can embed roundtrip client
 }

 func NewClient(addr, version string) (*MyClient, error) {
     c, err := roundtrip.NewClient(addr, version)
     if err != nil {
         return nil, err
     }
     return &MyClient{*c}, nil
 }
*/
package roundtrip

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

type ClientParam func(c *Client) error

// HTTPClient is a functional parameter that sets the internal
// HTTPClient of the roundtrip client wrapper
func HTTPClient(h *http.Client) ClientParam {
	return func(c *Client) error {
		c.client = h
		return nil
	}
}

// BasicAuth sets username and password for HTTP client
func BasicAuth(username, password string) ClientParam {
	return func(c *Client) error {
		c.basicAuth = &basicAuth{username: username, password: password}
		return nil
	}
}

// Client is a wrapper holding HTTP client. It hold target server address and a version prefix,
// and provides common features for building HTTP client wrappers.
type Client struct {
	// addr is target server address
	addr string
	// v is a version prefix
	v string
	// client is a private http.Client instance
	client *http.Client

	// basicAuth tells client to use HTTP basic auth on every request
	basicAuth *basicAuth
}

// NewClient returns a new instance of roundtrip.Client, or nil and error
//
//   c, err := NewClient("http://localhost:8080", "v1")
//   if err != nil {
//       // handle error
//   }
//
func NewClient(addr, v string, params ...ClientParam) (*Client, error) {
	c := &Client{
		addr:   addr,
		v:      v,
		client: http.DefaultClient,
	}
	for _, p := range params {
		if err := p(c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// HTTPClient returns underlying http.Client
func (c *Client) HTTPClient() *http.Client {
	return c.client
}

// Endpoint returns a URL constructed from parts and version appended, e.g.
//
// c.Endpoint("user", "john") // returns "/v1/users/john"
//
func (c *Client) Endpoint(params ...string) string {
	return fmt.Sprintf("%s/%s/%s", c.addr, c.v, strings.Join(params, "/"))
}

// PostForm posts urlencoded form with values and returns the result
//
// c.PostForm(c.Endpoint("users"), url.Values{"name": []string{"John"}})
//
func (c *Client) PostForm(endpoint string, vals url.Values, files ...File) (*Response, error) {
	return c.RoundTrip(func() (*http.Response, error) {
		if len(files) == 0 {
			req, err := http.NewRequest("POST", endpoint, strings.NewReader(vals.Encode()))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			c.addAuth(req)
			return c.client.Do(req)
		}
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)

		// write simple fields
		for name, vals := range vals {
			for _, val := range vals {
				if err := writer.WriteField(name, val); err != nil {
					return nil, err
				}
			}
		}

		// add files
		for _, f := range files {
			w, err := writer.CreateFormFile(f.Name, f.Filename)
			if err != nil {
				return nil, err
			}
			_, err = io.Copy(w, f.Reader)
			if err != nil {
				return nil, err
			}
		}
		boundary := writer.Boundary()
		if err := writer.Close(); err != nil {
			return nil, err
		}
		req, err := http.NewRequest("POST", endpoint, body)
		if err != nil {
			return nil, err
		}
		c.addAuth(req)
		req.Header.Set("Content-Type",
			fmt.Sprintf(`multipart/form-data;boundary="%v"`, boundary))
		return c.client.Do(req)
	})
}

// PostJSON posts JSON "application/json" encoded request body
//
// c.PostJSON(c.Endpoint("users"), map[string]string{"name": "alice@example.com"})
//
func (c *Client) PostJSON(endpoint string, data interface{}) (*Response, error) {
	return c.RoundTrip(func() (*http.Response, error) {
		data, err := json.Marshal(data)
		req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(data))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		c.addAuth(req)
		return c.client.Do(req)
	})
}

// PutJSON posts JSON "application/json" encoded request body and "PUT" method
//
// c.PutJSON(c.Endpoint("users"), map[string]string{"name": "alice@example.com"})
//
func (c *Client) PutJSON(endpoint string, data interface{}) (*Response, error) {
	return c.RoundTrip(func() (*http.Response, error) {
		data, err := json.Marshal(data)
		req, err := http.NewRequest("PUT", endpoint, bytes.NewBuffer(data))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		c.addAuth(req)
		return c.client.Do(req)
	})
}

// Delete executes DELETE request to the endpoint with no body
//
// re, err := c.Delete(c.Endpoint("users", "id1"))
//
func (c *Client) Delete(endpoint string) (*Response, error) {
	return c.RoundTrip(func() (*http.Response, error) {
		req, err := http.NewRequest("DELETE", endpoint, nil)
		if err != nil {
			return nil, err
		}
		c.addAuth(req)
		return c.client.Do(req)
	})
}

// Get executes GET request to the server endpoint with optional query arguments passed in params
//
// re, err := c.Get(c.Endpoint("users"), url.Values{"name": []string{"John"}})
//
func (c *Client) Get(u string, params url.Values) (*Response, error) {
	baseUrl, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	baseUrl.RawQuery = params.Encode()
	return c.RoundTrip(func() (*http.Response, error) {
		req, err := http.NewRequest("GET", baseUrl.String(), nil)
		if err != nil {
			return nil, err
		}
		c.addAuth(req)
		return c.client.Do(req)
	})
}

// GetFile executes get request and returns a file like object
//
// f, err := c.GetFile("files", "report.txt") // returns "/v1/files/report.txt"
//
func (c *Client) GetFile(u string, params url.Values) (*FileResponse, error) {
	baseUrl, err := url.Parse(u)
	if err != nil {
		return nil, err
	}
	baseUrl.RawQuery = params.Encode()
	req, err := http.NewRequest("GET", baseUrl.String(), nil)
	if err != nil {
		return nil, err
	}
	c.addAuth(req)
	re, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	return &FileResponse{
		code:    re.StatusCode,
		headers: re.Header,
		body:    re.Body,
	}, nil
}

// RoundTripFn inidicates any function that can be passed to RoundTrip
// it should return http response or error in case of error
type RoundTripFn func() (*http.Response, error)

func (c *Client) RoundTrip(fn RoundTripFn) (*Response, error) {
	re, err := fn()
	if err != nil {
		return nil, err
	}
	defer re.Body.Close()
	buf := &bytes.Buffer{}
	_, err = io.Copy(buf, re.Body)
	if err != nil {
		return nil, err
	}
	return &Response{code: re.StatusCode, headers: re.Header, body: buf}, nil
}

// SetAuthHeader sets client's authorization headers if client
// was configured to work with authorization
func (c *Client) SetAuthHeader(h http.Header) {
	if c.basicAuth != nil {
		h.Set("Authorization", c.basicAuth.String())
	}
}

func (c *Client) addAuth(r *http.Request) {
	if c.basicAuth != nil {
		r.SetBasicAuth(c.basicAuth.username, c.basicAuth.password)
	}
}

// Response indicates HTTP server response
type Response struct {
	code    int
	headers http.Header
	body    *bytes.Buffer
}

// Code returns HTTP response status code
func (r *Response) Code() int {
	return r.code
}

// Headers returns http.Header dictionary with response headers
func (r *Response) Headers() http.Header {
	return r.headers
}

// Reader returns reader with HTTP response body
func (r *Response) Reader() io.Reader {
	return r.body
}

// Bytes reads all http response body bytes in memory and returns the result
func (r *Response) Bytes() []byte {
	return r.body.Bytes()
}

// File is a file-like object that can be posted to the files
type File struct {
	Name     string
	Filename string
	Reader   io.Reader
}

// Response indicates HTTP server file response
type FileResponse struct {
	code    int
	headers http.Header
	body    io.ReadCloser
}

func (r *FileResponse) FileName() string {
	value := r.headers.Get("Content-Disposition")
	if len(value) == 0 {
		return ""
	}
	_, params, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return params["filename"]
}

// Code returns HTTP response status code
func (r *FileResponse) Code() int {
	return r.code
}

// Headers returns http.Header dictionary with response headers
func (r *FileResponse) Headers() http.Header {
	return r.headers
}

// Reader returns reader with HTTP response body
func (r *FileResponse) Body() io.ReadCloser {
	return r.body
}

func (r *FileResponse) Close() error {
	return r.body.Close()
}

type basicAuth struct {
	username string
	password string
}

func (b *basicAuth) String() string {
	auth := b.username + ":" + b.password
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
}
