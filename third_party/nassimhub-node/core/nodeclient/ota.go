package nodeclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/human-agent65535/nassimhub-node/proto"
)

// Carrying an agent update to a Node.
//
// The Core is the courier: it hands the device a signed manifest and then the
// bytes, and the device decides - against release keys the Core cannot change -
// whether any of it is acceptable. Nothing here verifies a signature on the
// Core's behalf of the device; a Core that wants to refuse a bad release before
// sending it does that with its own keyring, separately.

// ErrUpdatesUnsupported reports a Node that predates the update endpoints
// (protocol 1.3) or has no update service. Distinct from a refused update.
var ErrUpdatesUnsupported = errors.New("node does not support agent updates")

// ErrUploadOffset reports an upload that did not start where the device's copy
// ends. Offset is where the device says it is, so the caller can resume.
type ErrUploadOffset struct{ Offset int64 }

func (e *ErrUploadOffset) Error() string {
	return fmt.Sprintf("upload offset mismatch; the device holds %d bytes", e.Offset)
}

func unsupportedUpdate(err error) error {
	if operation, ok := proto.AsOperationError(err); ok {
		if operation.Code == proto.ErrorNotSupported ||
			(operation.Code == proto.ErrorNotFound && operation.Operation == "route") {
			return fmt.Errorf("%w: %s", ErrUpdatesUnsupported, operation.Message)
		}
	}
	return err
}

// UpdateStatus reads where the device's update machine is.
func (c *Client) UpdateStatus(ctx context.Context) (proto.OTADeviceStatus, error) {
	var status proto.OTADeviceStatus
	if err := c.call(ctx, http.MethodGet, proto.OTAStatusPath, nil, &status); err != nil {
		return proto.OTADeviceStatus{}, unsupportedUpdate(err)
	}
	return status, nil
}

// OfferUpdate presents a signed manifest. The response carries the manifest as
// the DEVICE verified it and how many artifact bytes it already holds.
func (c *Client) OfferUpdate(ctx context.Context, signed proto.OTASignedManifest) (proto.OTAOfferResponse, error) {
	var response proto.OTAOfferResponse
	if err := c.call(ctx, http.MethodPost, proto.OTAManifestPath, signed, &response); err != nil {
		return proto.OTAOfferResponse{}, unsupportedUpdate(err)
	}
	return response, nil
}

// UploadUpdate sends artifact bytes starting at offset. length is the number of
// bytes body will yield, or -1 when unknown.
//
// The request is not retried here. A retry has to re-read the status and
// resume from where the device actually is, which only the caller can do: it
// owns the source of the bytes.
func (c *Client) UploadUpdate(ctx context.Context, releaseID string, offset int64, body io.Reader, length int64) (proto.OTADeviceStatus, error) {
	if err := c.ensureSession(ctx); err != nil {
		return proto.OTADeviceStatus{}, unsupportedUpdate(err)
	}
	endpoint, err := c.selector.Active(ctx)
	if err != nil {
		return proto.OTADeviceStatus{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint.BaseURL+proto.OTAArtifactPath, body)
	if err != nil {
		return proto.OTADeviceStatus{}, err
	}
	if length >= 0 {
		request.ContentLength = length
	}
	c.mu.Lock()
	token := c.session.Token
	c.mu.Unlock()
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set(proto.HeaderOTARelease, releaseID)
	request.Header.Set(proto.HeaderOTAOffset, strconv.FormatInt(offset, 10))

	// The ordinary client carries a short overall timeout sized for small JSON
	// calls. A multi-megabyte upload over a slow link is bounded by the
	// caller's context instead.
	uploader := &http.Client{Transport: c.http.Transport, CheckRedirect: c.http.CheckRedirect}
	response, err := uploader.Do(request)
	if err != nil {
		c.selector.Invalidate()
		return proto.OTADeviceStatus{}, &url{err: err}
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return proto.OTADeviceStatus{}, &url{err: err}
	}
	if response.StatusCode >= 400 {
		if response.StatusCode == http.StatusConflict {
			if at, parseErr := strconv.ParseInt(response.Header.Get(proto.HeaderOTAOffset), 10, 64); parseErr == nil {
				return proto.OTADeviceStatus{}, &ErrUploadOffset{Offset: at}
			}
		}
		var failure proto.ErrorBody
		if json.Unmarshal(payload, &failure) == nil && failure.Error.Code != "" {
			return proto.OTADeviceStatus{}, unsupportedUpdate(&proto.OperationError{
				Code: failure.Error.Code, Operation: failure.Error.Operation, Message: failure.Error.Message,
			})
		}
		return proto.OTADeviceStatus{}, &proto.OperationError{
			Code: codeForStatus(response.StatusCode), Message: fmt.Sprintf("node returned %d", response.StatusCode),
		}
	}
	var status proto.OTADeviceStatus
	if err := json.Unmarshal(payload, &status); err != nil {
		return proto.OTADeviceStatus{}, fmt.Errorf("decode node response: %w", err)
	}
	return status, nil
}

// ApplyUpdate switches the device to the validated release. The device answers
// and then restarts; the caller observes the restart through the ordinary poll.
func (c *Client) ApplyUpdate(ctx context.Context, releaseID string) (proto.OTADeviceStatus, error) {
	return c.updateAction(ctx, proto.OTAApplyPath, releaseID)
}

// ConfirmUpdate tells the device its new agent works.
func (c *Client) ConfirmUpdate(ctx context.Context, releaseID string) (proto.OTADeviceStatus, error) {
	return c.updateAction(ctx, proto.OTAConfirmPath, releaseID)
}

// RollbackUpdate undoes an applied, unconfirmed update.
func (c *Client) RollbackUpdate(ctx context.Context, releaseID string) (proto.OTADeviceStatus, error) {
	return c.updateAction(ctx, proto.OTARollbackPath, releaseID)
}

func (c *Client) updateAction(ctx context.Context, path, releaseID string) (proto.OTADeviceStatus, error) {
	var status proto.OTADeviceStatus
	if err := c.call(ctx, http.MethodPost, path, proto.OTAReleaseRequest{ReleaseID: releaseID}, &status); err != nil {
		return proto.OTADeviceStatus{}, unsupportedUpdate(err)
	}
	return status, nil
}
