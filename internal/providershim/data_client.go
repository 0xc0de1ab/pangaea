package providershim

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/0xc0de1ab/pangaea/internal/compat"
	"github.com/0xc0de1ab/pangaea/internal/provider"
	"github.com/0xc0de1ab/pangaea/internal/providersim"
	"github.com/0xc0de1ab/pangaea/internal/tunnel"
	"github.com/gorilla/websocket"
	"golang.org/x/sync/errgroup"
)

type SimulatorShimOptions struct {
	ControlURL        string
	DataURL           string
	HeartbeatInterval time.Duration
	TokenKey          []byte
	Simulator         *providersim.Simulator
}

type DataClientOptions struct {
	DataURL  string
	TokenKey []byte
	Provider providerInvoker
}

type providerInvoker interface {
	Registration() (provider.Registration, error)
	Invoke(context.Context, provider.Registration, compat.Request) (compat.Response, error)
}

func RunSimulatorShim(ctx context.Context, opts SimulatorShimOptions) error {
	if opts.ControlURL == "" {
		return fmt.Errorf("%w: control url is required", ErrShimConfig)
	}
	if opts.Simulator == nil {
		return fmt.Errorf("%w: simulator is required", ErrShimConfig)
	}
	registration, err := opts.Simulator.Registration()
	if err != nil {
		return err
	}
	dataURL := opts.DataURL
	if dataURL == "" {
		dataURL, err = DeriveDataURL(opts.ControlURL, registration.Identity.ProviderInstanceID)
		if err != nil {
			return err
		}
	}

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return RunSimulatorControlClient(ctx, ControlClientOptions{
			ControlURL:        opts.ControlURL,
			HeartbeatInterval: opts.HeartbeatInterval,
			Simulator:         opts.Simulator,
		})
	})
	eg.Go(func() error {
		return RunSimulatorDataClient(ctx, DataClientOptions{
			DataURL:  dataURL,
			TokenKey: opts.TokenKey,
			Provider: opts.Simulator,
		})
	})
	return eg.Wait()
}

func RunSimulatorDataClient(ctx context.Context, opts DataClientOptions) error {
	if opts.DataURL == "" {
		return fmt.Errorf("%w: data url is required", ErrShimConfig)
	}
	if opts.Provider == nil {
		return fmt.Errorf("%w: provider is required", ErrShimConfig)
	}
	signer, err := tunnel.NewTokenSigner(opts.TokenKey)
	if err != nil {
		return err
	}
	registration, err := opts.Provider.Registration()
	if err != nil {
		return err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, opts.DataURL, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer conn.Close()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "context done"), time.Now().Add(time.Second))
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	for {
		var request tunnel.DataRequest
		if err := conn.ReadJSON(&request); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		response := handleDataRequest(ctx, signer, registration, opts.Provider, request)
		if err := conn.WriteJSON(response); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

func DeriveDataURL(controlURL string, providerInstanceID string) (string, error) {
	if strings.TrimSpace(providerInstanceID) == "" {
		return "", fmt.Errorf("%w: provider instance id is required", ErrShimConfig)
	}
	u, err := url.Parse(controlURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	if strings.Contains(u.Path, "/control/ws") {
		u.Path = strings.Replace(u.Path, "/control/ws", "/data/ws", 1)
	} else {
		u.Path = "/router/v1/data/ws"
	}
	q := u.Query()
	q.Set("provider_instance_id", providerInstanceID)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func handleDataRequest(ctx context.Context, signer *tunnel.TokenSigner, registration provider.Registration, invoker providerInvoker, request tunnel.DataRequest) tunnel.DataResponse {
	response := tunnel.DataResponse{RequestID: request.RequestID, StreamID: request.Descriptor.StreamID}
	if err := request.Descriptor.Validate(); err != nil {
		response.Error = err.Error()
		return response
	}
	claims, err := signer.VerifyForDescriptor(request.CapabilityToken, request.Descriptor, time.Now().UTC())
	if err != nil {
		response.Error = err.Error()
		return response
	}
	if claims.RequestID != request.RequestID {
		response.Error = fmt.Sprintf("%s: token request_id %q does not match frame request_id %q", ErrShimConfig, claims.RequestID, request.RequestID)
		return response
	}
	compatResponse, err := invoker.Invoke(ctx, registration, request.Request)
	if err != nil {
		response.Error = err.Error()
		return response
	}
	response.Response = compatResponse
	return response
}
