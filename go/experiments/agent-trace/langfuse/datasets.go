package agent_trace_langfuse

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

const (
	datasetsEndpointPath     = "/api/public/v2/datasets"
	datasetItemsEndpointPath = "/api/public/dataset-items"
)

// DatasetClient 将本地评估数据同步到 Langfuse 的 Dataset API。
// 它只负责创建数据集和 upsert 数据集项目，不负责运行实验或计算分数。
type DatasetClient struct {
	baseURL    string
	publicKey  string
	secretKey  string
	httpClient *http.Client
}

// Dataset 是 Langfuse 中托管的数据集记录。
type Dataset struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// DatasetItem 是一个可重复 upsert 的 Langfuse 数据集项目。
// ID 使用本地案例 ID，保证重复运行时更新同一个项目而不是不断新增项目。
type DatasetItem struct {
	DatasetName    string         `json:"datasetName"`
	ID             string         `json:"id"`
	Input          any            `json:"input,omitempty"`
	ExpectedOutput any            `json:"expectedOutput,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
}

// NewDatasetClient 创建一个使用 Langfuse Public API 的 Dataset 客户端。
func NewDatasetClient(
	baseURL string,
	publicKey string,
	secretKey string,
	timeout time.Duration,
) (*DatasetClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, errors.New("Langfuse base URL is required for dataset sync")
	}
	if strings.TrimSpace(publicKey) == "" || strings.TrimSpace(secretKey) == "" {
		return nil, errors.New("Langfuse public and secret keys are required for dataset sync")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	return &DatasetClient{
		baseURL:    baseURL,
		publicKey:  strings.TrimSpace(publicKey),
		secretKey:  strings.TrimSpace(secretKey),
		httpClient: &http.Client{Timeout: timeout},
	}, nil
}

// Sync 确保数据集存在，并按项目 ID upsert 全部案例。
// 同步顺序固定且遇到首个 API 错误即返回；已成功的项目可在下次运行时复用。
func (c *DatasetClient) Sync(
	ctx context.Context,
	name string,
	description string,
	metadata map[string]any,
	items []DatasetItem,
) (Dataset, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Dataset{}, errors.New("dataset name is required")
	}

	dataset, err := c.ensureDataset(ctx, Dataset{
		Name:        name,
		Description: description,
		Metadata:    metadata,
	})
	if err != nil {
		return Dataset{}, err
	}

	for _, item := range items {
		if item.DatasetName == "" {
			item.DatasetName = name
		}
		if item.DatasetName != name {
			return Dataset{}, fmt.Errorf(
				"dataset item %q belongs to %q, expected %q",
				item.ID,
				item.DatasetName,
				name,
			)
		}
		if err := c.UpsertItem(ctx, item); err != nil {
			return Dataset{}, fmt.Errorf("sync dataset item %q: %w", item.ID, err)
		}
	}

	return dataset, nil
}

// UpsertItem 创建或更新一个 Langfuse 数据集项目。
func (c *DatasetClient) UpsertItem(ctx context.Context, item DatasetItem) error {
	if strings.TrimSpace(item.DatasetName) == "" {
		return errors.New("dataset item datasetName is required")
	}
	if strings.TrimSpace(item.ID) == "" {
		return errors.New("dataset item id is required")
	}

	status, body, err := c.doJSON(
		ctx,
		http.MethodPost,
		c.baseURL+datasetItemsEndpointPath,
		item,
	)
	if err != nil {
		return fmt.Errorf("send Langfuse dataset item: %w", err)
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return datasetAPIError(status, body)
	}
	return nil
}

func (c *DatasetClient) ensureDataset(
	ctx context.Context,
	wanted Dataset,
) (Dataset, error) {
	endpoint := c.baseURL + datasetsEndpointPath + "/" + url.PathEscape(wanted.Name)
	status, body, err := c.doJSON(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Dataset{}, fmt.Errorf("get Langfuse dataset: %w", err)
	}
	if status >= http.StatusOK && status < http.StatusMultipleChoices {
		return decodeDataset(body, wanted.Name)
	}
	if status != http.StatusNotFound {
		return Dataset{}, datasetAPIError(status, body)
	}

	status, body, err = c.doJSON(
		ctx,
		http.MethodPost,
		c.baseURL+datasetsEndpointPath,
		wanted,
	)
	if err != nil {
		return Dataset{}, fmt.Errorf("create Langfuse dataset: %w", err)
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return Dataset{}, datasetAPIError(status, body)
	}
	return decodeDataset(body, wanted.Name)
}

func decodeDataset(body []byte, fallbackName string) (Dataset, error) {
	var dataset Dataset
	if err := json.Unmarshal(body, &dataset); err != nil {
		return Dataset{}, fmt.Errorf("decode Langfuse dataset: %w", err)
	}
	if dataset.Name == "" {
		dataset.Name = fallbackName
	}
	return dataset, nil
}

func (c *DatasetClient) doJSON(
	ctx context.Context,
	method string,
	endpoint string,
	payload any,
) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, fmt.Errorf("marshal Langfuse dataset request: %w", err)
		}
		body = bytes.NewReader(data)
	}

	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return 0, nil, fmt.Errorf("create Langfuse dataset request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth(c.publicKey, c.secretKey)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return 0, nil, err
	}

	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	closeErr := response.Body.Close()
	if readErr != nil {
		return response.StatusCode, nil, fmt.Errorf("read Langfuse dataset response: %w", readErr)
	}
	if closeErr != nil {
		return response.StatusCode, nil, fmt.Errorf("close Langfuse dataset response: %w", closeErr)
	}
	return response.StatusCode, responseBody, nil
}

func datasetAPIError(status int, body []byte) error {
	return fmt.Errorf(
		"Langfuse dataset API status=%d body=%s",
		status,
		truncate(string(body), 1000),
	)
}
