package setup

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

type Client struct {
	Inner         http.Client
	Username      string
	Password      string
	Elasticsearch string
	Kibana        string
}

func (c *Client) ReqElasticsearch(method string, path string, body io.Reader) (int, io.ReadCloser, error) {
	url := fmt.Sprintf("%s%s", c.Elasticsearch, path)

	// Build request
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to build Elasticsearch request to '%s': %s", path, err)
	}
	req.SetBasicAuth(c.Username, c.Password)
	req.Header.Set("kbn-xsrf", "true")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Send request
	res, err := c.Inner.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to send Elasticsearch request to '%s': %s", path, err)
	}
	return res.StatusCode, res.Body, nil
}

func (c *Client) ReqKibana(method string, path string, body io.Reader) (int, io.ReadCloser, error) {
	url := fmt.Sprintf("%s%s", c.Kibana, path)

	// Build request
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to build Kibana request to '%s': %s", path, err)
	}
	req.SetBasicAuth(c.Username, c.Password)
	req.Header.Set("kbn-xsrf", "true")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Send request
	res, err := c.Inner.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to send Kibana request to '%s': %s", path, err)
	}
	return res.StatusCode, res.Body, nil
}

func (c *Client) Wait() error {
	first := true
	for {
		// If we haven't been through this loop yet, sleep for 5 seconds
		if !first {
			zap.S().Info("waiting for Elasticsearch to be ready...")
			time.Sleep(5 * time.Second)
		}
		first = false

		_, body, err := c.ReqElasticsearch("GET", "/_cluster/health", nil)
		if err != nil {
			continue
		}

		// Check if response status is "green"
		health := struct {
			Status string `json:"status"`
		}{}
		decoder := json.NewDecoder(body)
		err = decoder.Decode(&health)
		if err != nil {
			continue
		}
		body.Close()
		if health.Status == "green" {
			break
		}
	}

	first = true
	for {
		// If we haven't been through this loop yet, sleep for 5 seconds
		if !first {
			zap.S().Info("waiting for Kibana to be ready...")
			time.Sleep(5 * time.Second)
		}
		first = false

		_, body, err := c.ReqKibana("GET", "/api/status", nil)
		if err != nil {
			continue
		}

		// Check if response status is "green"
		health := struct {
			Status struct {
				Overall struct {
					State string `json:"state"`
				} `json:"overall"`
			} `json:"status"`
		}{}
		decoder := json.NewDecoder(body)
		err = decoder.Decode(&health)
		if err != nil {
			continue
		}
		body.Close()
		if health.Status.Overall.State == "green" {
			break
		}
	}

	return nil
}

func CloseAndCheck(code int, body io.ReadCloser, err error) error {
	if err != nil {
		return err
	}
	defer body.Close()
	if code != 200 && code != 204 {
		buf := new(strings.Builder)
		_, err := io.Copy(buf, body)
		if err != nil {
			return fmt.Errorf("got %v response code and couldn't read response body: %s", code, err)
		}
		return fmt.Errorf("response code was %v - response body: %s", code, buf)
	}

	return nil
}

func (c *Client) AddDashboard(data func() io.Reader) error {
	zap.S().Info("adding dashboards")
	err := CloseAndCheck(c.ReqKibana("POST", "/api/kibana/dashboards/import?force=true", data()))
	if err != nil {
		return err
	}

	return CloseAndCheck(c.ReqKibana("POST", "/s/scorestack/api/kibana/dashboards/import?force=true", data()))
}

func (c *Client) AddIndex(name string, data func() io.Reader) error {
	url := fmt.Sprintf("/%s", name)

	// Don't create the index if it already exists
	code, b, err := c.ReqElasticsearch("GET", url, data())

	if code == 404 {
		zap.S().Infof("adding index: %s", name)
		return CloseAndCheck(c.ReqElasticsearch("PUT", fmt.Sprintf("/%s", name), data()))
	}

	zap.S().Infof("index '%s' already exists, skipping...", name)
	return CloseAndCheck(code, b, err)
}

func (c *Client) AddRole(name string, data io.Reader) error {
	zap.S().Infof("adding role: %s", name)
	return CloseAndCheck(c.ReqKibana("PUT", fmt.Sprintf("/api/security/role/%s", name), data))
}

func (c *Client) AddSpace(name string, data func() io.Reader) error {
	// Try to update the space if it already exists
	code, b, err := c.ReqKibana("PUT", fmt.Sprintf("/api/spaces/space/%s", name), data())
	if code == 404 {
		// If the space doesn't exist, create it
		zap.S().Infof("adding Kibana space: %s", name)
		return CloseAndCheck(c.ReqKibana("POST", "/api/spaces/space", data()))
	}

	zap.S().Infof("Kibana space '%s' already exists, skipping...", name)
	return CloseAndCheck(code, b, err)
}

func (c *Client) AddUser(name string, data io.Reader) error {
	url := fmt.Sprintf("/_security/user/%s", name)

	// Don't try to create the user if they exist already
	code, b, err := c.ReqElasticsearch("GET", url, nil)

	if code == 404 {
		zap.S().Infof("adding user: %s", name)
		return CloseAndCheck(c.ReqElasticsearch("PUT", url, data))
	}

	zap.S().Infof("user '%s' already exists, skipping...", name)
	return CloseAndCheck(code, b, err)
}
