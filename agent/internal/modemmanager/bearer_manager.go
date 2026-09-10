package modemmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/human-agent65535/modemdeck/agent/internal/domain"
)

const (
	bearerPollInterval    = 200 * time.Millisecond
	bearerCleanupTimeout  = 10 * time.Second
	bearerRecoveryTimeout = 30 * time.Second
)

// ReconcileDataPlane restores only network state backed by an exact
// ModemDeck-owned bearer in the current ModemManager process. Stale records
// from a previous D-Bus owner are released without touching modem objects.
func (p *Provider) ReconcileDataPlane(ctx context.Context) error {
	const operation = "reconcile_data_plane"
	if _, err := p.resolveProviderIdentity(ctx, operation); err != nil {
		return err
	}
	providerEpoch := p.ids.providerEpoch()
	ownedLines := make(map[string]struct{})
	for _, owned := range p.ownedBearers.list() {
		if owned.ProviderEpoch == providerEpoch {
			ownedLines[owned.LineID] = struct{}{}
		}
	}
	for _, lineID := range p.dataPlane.OwnedLines() {
		if _, found := ownedLines[lineID]; found {
			continue
		}
		if err := p.dataPlane.Release(ctx, lineID); err != nil {
			return domain.VerificationFailed(
				operation,
				"orphaned ModemDeck network state could not be removed",
				err,
			)
		}
	}
	for _, owned := range p.ownedBearers.list() {
		if err := ctx.Err(); err != nil {
			return domain.Unavailable(operation, "data plane reconciliation was canceled", err)
		}
		if owned.ProviderEpoch != providerEpoch {
			if err := p.releaseStaleOwnedBearer(owned.LineID); err != nil {
				return domain.VerificationFailed(
					operation,
					"stale network state could not be removed",
					err,
				)
			}
			continue
		}
		connection, found, err := p.readBearerConnection(
			ctx,
			owned.BearerPath,
			operation,
		)
		if err != nil {
			return err
		}
		if !found || !connection.Connected {
			if err := p.dataPlane.Release(ctx, owned.LineID); err != nil {
				return domain.VerificationFailed(
					operation,
					"disconnected bearer network state could not be removed",
					err,
				)
			}
			if !found {
				if err := p.ownedBearers.remove(owned.LineID); err != nil {
					return domain.VerificationFailed(
						operation,
						"missing bearer ownership state could not be removed",
						err,
					)
				}
			}
			continue
		}
		if !bearerConnectionReady(connection) {
			return domain.FailedPrecondition(
				operation,
				fmt.Sprintf(
					"owned bearer %s is connected without complete IP configuration",
					owned.BearerPath,
				),
				nil,
			)
		}
		if err := p.configureDataPlane(ctx, owned.LineID, connection); err != nil {
			return domain.FailedPrecondition(
				operation,
				fmt.Sprintf(
					"owned bearer %s network state could not be restored",
					owned.BearerPath,
				),
				err,
			)
		}
	}
	return nil
}

func (p *Provider) activateOwnedBearerData(
	ctx context.Context,
	modemPath dbus.ObjectPath,
	lineID string,
	requestedAPN string,
	automaticAPN string,
	ipFamily uint32,
	reuseExisting bool,
) (dbus.ObjectPath, error) {
	const operation = "apply_device_configuration"
	providerEpoch := p.ids.providerEpoch()
	if providerEpoch == "" {
		return "", domain.Unavailable(
			operation,
			"ModemManager owner identity is unavailable",
			nil,
		)
	}
	if saved, found := p.ownedBearers.getAny(lineID); found &&
		saved.ProviderEpoch != providerEpoch {
		if err := p.releaseStaleOwnedBearer(lineID); err != nil {
			return "", domain.VerificationFailed(
				operation,
				"network state from a previous ModemManager process could not be removed",
				err,
			)
		}
	}

	effectiveAPN := strings.TrimSpace(requestedAPN)
	if effectiveAPN == "" {
		effectiveAPN = strings.TrimSpace(automaticAPN)
	}
	if owned, found := p.ownedBearers.get(lineID, providerEpoch); found {
		if owned.ModemPath != modemPath {
			return "", domain.Conflict(
				operation,
				"the saved ModemDeck bearer belongs to a different modem path",
			)
		}
		connection, found, err := p.readBearerConnection(
			ctx,
			owned.BearerPath,
			operation,
		)
		if err != nil {
			return "", err
		}
		if !found {
			if err := p.releaseStaleOwnedBearer(lineID); err != nil {
				return "", domain.VerificationFailed(
					operation,
					"stale ModemDeck bearer state could not be removed",
					err,
				)
			}
		} else if reuseExisting &&
			connection.Connected &&
			dataConnectionMatches(connection, effectiveAPN, ipFamily) {
			if err := p.configureDataPlane(ctx, lineID, connection); err != nil {
				return "", domain.FailedPrecondition(
					operation,
					"the existing ModemDeck bearer network state could not be restored",
					err,
				)
			}
			return owned.BearerPath, nil
		} else {
			if _, err := p.deactivateOwnedBearerData(ctx, modemPath, lineID); err != nil {
				return "", err
			}
		}
	}

	objects, err := p.managedObjects(ctx, operation)
	if err != nil {
		return "", err
	}
	if conflict, conflictConnection, found, err := p.connectedAttachedBearer(
		ctx,
		objects,
		modemPath,
		operation,
	); err != nil {
		return "", err
	} else if found {
		// A bearer the modem attached itself already carries this line's
		// default traffic, which is exactly the state connect_data is asking
		// for. Adopt it: record the ownership so a later disconnect_data can
		// manage it, and bring the host data plane up on the same connection.
		// Only reject it when it plainly belongs to a different request.
		if dataConnectionMatches(conflictConnection, effectiveAPN, ipFamily) {
			adopted := ownedBearer{
				LineID:        lineID,
				ProviderEpoch: providerEpoch,
				ModemPath:     modemPath,
				BearerPath:    dbus.ObjectPath(conflict),
				APN:           strings.TrimSpace(conflictConnection.APN),
				IPFamily:      ipFamily,
			}
			if err := p.ownedBearers.put(adopted); err != nil {
				return "", domain.VerificationFailed(
					operation,
					"the modem's attached data bearer could not be recorded as ModemDeck-owned",
					err,
				)
			}
			if err := p.configureDataPlane(ctx, lineID, conflictConnection); err != nil {
				// The ownership record must not outlive a data plane that was
				// never configured, otherwise the next disconnect_data would
				// try to tear down state it never created.
				if removeErr := p.ownedBearers.remove(lineID); removeErr != nil {
					err = errors.Join(err, removeErr)
				}
				return "", domain.FailedPrecondition(
					operation,
					"the modem's attached data bearer could not be configured in the host network namespace",
					err,
				)
			}
			return adopted.BearerPath, nil
		}
		return "", domain.Conflict(
			operation,
			fmt.Sprintf(
				"the modem already has a connected data bearer not owned by ModemDeck (%s)",
				conflict,
			),
		)
	}

	// Bound the activation itself. The caller's context covers the whole write
	// path (and the configMu hold); this shorter deadline keeps a network that
	// refuses the PDP context from turning into a multi-minute stall.
	boundedBearer, cancelBearer, err := configurationContext(
		ctx,
		bearerActivationTimeout,
		operation,
	)
	if err != nil {
		return "", err
	}
	defer cancelBearer()

	properties := bearerProperties(requestedAPN, automaticAPN, ipFamily)
	body, err := p.call(
		ctx,
		modemPath,
		modemInterface+".CreateBearer",
		operation,
		"ModemManager failed to create the packet data bearer",
		properties,
	)
	if err != nil {
		return "", err
	}
	var bearerPath dbus.ObjectPath
	if err := dbus.Store(body, &bearerPath); err != nil {
		return "", domain.Internal(
			operation,
			"ModemManager returned a malformed bearer path",
			err,
		)
	}
	if !bearerPath.IsValid() || bearerPath == "/" {
		return "", domain.Internal(
			operation,
			"ModemManager returned an invalid bearer path",
			nil,
		)
	}

	owned := ownedBearer{
		LineID:        lineID,
		ProviderEpoch: providerEpoch,
		ModemPath:     modemPath,
		BearerPath:    bearerPath,
		APN:           effectiveAPN,
		IPFamily:      ipFamily,
	}
	if err := p.ownedBearers.put(owned); err != nil {
		cleanupErr := p.deleteUnconnectedBearer(modemPath, bearerPath)
		return "", domain.VerificationFailed(
			operation,
			"the created bearer could not be recorded as ModemDeck-owned",
			errors.Join(err, cleanupErr),
		)
	}

	if _, err := p.call(
		boundedBearer,
		bearerPath,
		bearerInterface+".Connect",
		operation,
		"ModemManager failed to connect the packet data bearer",
	); err != nil {
		if operationError, ok := domain.AsOperationError(err); ok &&
			operationError.Code == domain.ErrorNetworkRejected &&
			effectiveAPN != "" {
			err = domain.NewOperationError(
				operationError.Code,
				operation,
				fmt.Sprintf("the mobile network rejected APN %q", effectiveAPN),
				err,
			)
		}
		return "", p.activationFailureWithCleanup(modemPath, lineID, err)
	}

	connection, err := p.waitBearerReady(boundedBearer, bearerPath, operation)
	if err != nil {
		return "", p.activationFailureWithCleanup(modemPath, lineID, err)
	}
	if !dataConnectionMatches(connection, effectiveAPN, ipFamily) {
		verificationErr := errors.New(
			"ModemManager connected a bearer that does not match the requested APN and IP family",
		)
		return "", p.activationFailureWithCleanup(
			modemPath,
			lineID,
			domain.VerificationFailed(
				operation,
				verificationErr.Error(),
				verificationErr,
			),
		)
	}
	if err := p.configureDataPlane(ctx, lineID, connection); err != nil {
		return "", p.activationFailureWithCleanup(
			modemPath,
			lineID,
			domain.FailedPrecondition(
				operation,
				"the connected bearer could not be configured in the host network namespace",
				err,
			),
		)
	}
	return bearerPath, nil
}

func (p *Provider) deactivateOwnedBearerData(
	ctx context.Context,
	modemPath dbus.ObjectPath,
	lineID string,
) (bool, error) {
	const operation = "apply_device_configuration"
	if saved, found := p.ownedBearers.getAny(lineID); found &&
		saved.ProviderEpoch != p.ids.providerEpoch() {
		if err := p.releaseStaleOwnedBearer(lineID); err != nil {
			return false, domain.VerificationFailed(
				operation,
				"network state from a previous ModemManager process could not be removed",
				err,
			)
		}
		return false, nil
	}
	owned, found := p.ownedBearers.get(lineID, p.ids.providerEpoch())
	if !found {
		return false, nil
	}
	if owned.ModemPath != modemPath {
		return false, domain.Conflict(
			operation,
			"the saved ModemDeck bearer belongs to a different modem path",
		)
	}
	connection, bearerFound, err := p.readBearerConnection(
		ctx,
		owned.BearerPath,
		operation,
	)
	if err != nil {
		return false, err
	}
	if err := p.dataPlane.Release(ctx, lineID); err != nil {
		return false, domain.VerificationFailed(
			operation,
			"the ModemDeck bearer network state could not be released before disconnect",
			err,
		)
	}
	if bearerFound && connection.Connected {
		if _, err := p.call(
			ctx,
			owned.BearerPath,
			bearerInterface+".Disconnect",
			operation,
			"ModemManager failed to disconnect the packet data bearer",
		); err != nil && !operationErrorIsNotFound(err) {
			return false, err
		}
	}
	if _, err := p.call(
		ctx,
		modemPath,
		modemInterface+".DeleteBearer",
		operation,
		"ModemManager failed to delete the packet data bearer",
		owned.BearerPath,
	); err != nil && !operationErrorIsNotFound(err) {
		return false, err
	}
	if err := p.ownedBearers.remove(lineID); err != nil {
		return false, domain.VerificationFailed(
			operation,
			"the disconnected bearer ownership record could not be removed",
			err,
		)
	}
	return true, nil
}

func (p *Provider) waitBearerReady(
	ctx context.Context,
	bearerPath dbus.ObjectPath,
	operation string,
) (domain.DataConnection, error) {
	ticker := time.NewTicker(bearerPollInterval)
	defer ticker.Stop()
	for {
		connection, found, err := p.readBearerConnection(ctx, bearerPath, operation)
		if err != nil {
			return domain.DataConnection{}, err
		}
		if !found {
			return domain.DataConnection{}, domain.VerificationFailed(
				operation,
				"the packet data bearer disappeared during activation",
				nil,
			)
		}
		if bearerConnectionReady(connection) {
			return connection, nil
		}
		select {
		case <-ctx.Done():
			return domain.DataConnection{}, domain.Unavailable(
				operation,
				"ModemManager bearer activation timed out before IP configuration became available",
				ctx.Err(),
			)
		case <-ticker.C:
		}
	}
}

func (p *Provider) readBearerConnection(
	ctx context.Context,
	bearerPath dbus.ObjectPath,
	operation string,
) (domain.DataConnection, bool, error) {
	properties, found, err := p.referencedBearerProperties(
		ctx,
		nil,
		bearerPath,
		operation,
	)
	if err != nil || !found {
		return domain.DataConnection{}, found, err
	}
	connection, err := parseDataConnection(p.ids.bearerID(bearerPath), properties)
	if err != nil {
		return domain.DataConnection{}, false, domain.Internal(
			operation,
			"ModemManager bearer properties were malformed",
			err,
		)
	}
	return connection, true, nil
}

func (p *Provider) connectedExternalDefaultBearer(
	ctx context.Context,
	objects ManagedObjects,
	modemPath dbus.ObjectPath,
	operation string,
) (string, bool, error) {
	foreign, _, found, err := p.connectedAttachedBearer(ctx, objects, modemPath, operation)
	return foreign, found, err
}

// connectedAttachedBearer looks for a default-data bearer the modem already
// has connected but that ModemDeck did not create. It reports the bearer path
// and its parsed connection so the caller can decide between adopting it and
// rejecting it.
//
// The attach-time default bearer (MM_BEARER_TYPE_DEFAULT_ATTACH) is the data
// path a bridged module such as the QDC507 actually uses: the vendor ECM
// netdev answers DHCP and forwards LTE traffic as soon as the modem attaches,
// long before anyone asks ModemManager to activate a bearer. Refusing to touch
// it would leave the toggle permanently unable to report or manage a data
// connection that is demonstrably up.
func (p *Provider) connectedAttachedBearer(
	ctx context.Context,
	objects ManagedObjects,
	modemPath dbus.ObjectPath,
	operation string,
) (string, domain.DataConnection, bool, error) {
	interfaces, found := objects[modemPath]
	if !found {
		return "", domain.DataConnection{}, false, domain.NotFound(operation, "modem object was not found")
	}
	modemProperties, found := interfaces[modemInterface]
	if !found {
		return "", domain.DataConnection{}, false, domain.NotSupported(operation, "modem interface is unavailable")
	}
	paths, _ := objectPathValuesProperty(modemProperties, "Bearers")
	for _, path := range paths {
		properties, found, err := p.referencedBearerProperties(
			ctx,
			objects,
			path,
			operation,
		)
		if err != nil {
			return "", domain.DataConnection{}, false, err
		}
		if !found {
			continue
		}
		connection, err := parseDataConnection(p.ids.bearerID(path), properties)
		if err != nil {
			return "", domain.DataConnection{}, false, domain.Internal(
				operation,
				"ModemManager bearer properties were malformed",
				err,
			)
		}
		if connection.Connected && dataConnectionIsDefault(connection) {
			return string(path), connection, true, nil
		}
	}
	return "", domain.DataConnection{}, false, nil
}

func (p *Provider) activationFailureWithCleanup(
	modemPath dbus.ObjectPath,
	lineID string,
	activationErr error,
) error {
	cleanupErr := p.cleanupOwnedBearerData(modemPath, lineID)
	// An abandoned activation can leave the modem mid-transition, where it
	// refuses every later data request. Recover before returning so the next
	// attempt starts from a settled modem instead of inheriting the wedge.
	_, recoveryErr := p.recoverWedgedBearerState(modemPath)
	joined := errors.Join(activationErr, cleanupErr, recoveryErr)
	if cleanupErr == nil && recoveryErr == nil {
		return activationErr
	}
	return domain.VerificationFailed(
		"apply_device_configuration",
		"packet data activation failed and ModemDeck bearer cleanup or modem recovery also failed",
		joined,
	)
}

// recoverWedgedBearerState clears a ModemManager modem that is stuck
// mid-transition after a failed bearer activation.
//
// When a Connect call is abandoned (our bounded deadline, the user pressing
// the toggle again, or ModemManager's own "Operation was cancelled"), the
// modem can be left in the transient "connecting" state. ModemManager then
// refuses to service further data requests, and even AT commands stop
// answering until the transition resolves on its own — measured at 45 s for
// connecting and 136 s for disconnecting on the QDC507. Cycling the modem
// power state forces the transition to resolve and the modem returns to
// "registered" in roughly ten seconds.
//
// The recovery is best-effort and idempotent: it only acts when the modem is
// not in a settled state, and a failure to recover is reported alongside the
// original error rather than replacing it.
// recoverWedgedBearerState clears a ModemManager modem that is stuck
// mid-transition. It reports whether it actually cycled the modem, so callers
// can refresh the state they cached before the recovery.
func (p *Provider) recoverWedgedBearerState(modemPath dbus.ObjectPath) (bool, error) {
	const operation = "recover_bearer_state"
	ctx, cancel := context.WithTimeout(context.Background(), bearerRecoveryTimeout)
	defer cancel()
	objects, err := p.managedObjects(ctx, operation)
	if err != nil {
		return false, err
	}
	interfaces, found := objects[modemPath]
	if !found {
		return false, nil
	}
	properties, found := interfaces[modemInterface]
	if !found {
		return false, nil
	}
	stateCode, known := uint32Property(properties, "State")
	if !known || !modemStateIsTransient(stateCode) {
		return false, nil
	}
	var result error
	for _, powerState := range []uint32{modemPowerStateLow, modemPowerStateOn} {
		if _, err := p.call(
			ctx,
			modemPath,
			modemInterface+".SetPowerState",
			operation,
			"ModemManager failed to cycle the modem power state during recovery",
			powerState,
		); err != nil {
			result = errors.Join(result, err)
			continue
		}
	}
	return true, result
}

func modemStateIsTransient(code uint32) bool {
	switch code {
	case modemStateConnecting, modemStateDisconnecting:
		return true
	default:
		return false
	}
}

func (p *Provider) cleanupOwnedBearerData(
	modemPath dbus.ObjectPath,
	lineID string,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), bearerCleanupTimeout)
	defer cancel()
	_, err := p.deactivateOwnedBearerData(ctx, modemPath, lineID)
	return err
}

func (p *Provider) releaseStaleOwnedBearer(lineID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), bearerCleanupTimeout)
	defer cancel()
	if err := p.dataPlane.Release(ctx, lineID); err != nil {
		return err
	}
	return p.ownedBearers.remove(lineID)
}

func (p *Provider) deleteUnconnectedBearer(
	modemPath dbus.ObjectPath,
	bearerPath dbus.ObjectPath,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), bearerCleanupTimeout)
	defer cancel()
	_, err := p.call(
		ctx,
		modemPath,
		modemInterface+".DeleteBearer",
		"apply_device_configuration",
		"ModemManager failed to delete an unrecorded packet data bearer",
		bearerPath,
	)
	if operationErrorIsNotFound(err) {
		return nil
	}
	return err
}

func bearerProperties(
	requestedAPN string,
	automaticAPN string,
	ipFamily uint32,
) map[string]dbus.Variant {
	properties := map[string]dbus.Variant{
		"apn-type":      dbus.MakeVariant(domain.APNTypeDefault),
		"allow-roaming": dbus.MakeVariant(true),
	}
	apn := strings.TrimSpace(requestedAPN)
	if apn == "" {
		apn = strings.TrimSpace(automaticAPN)
	}
	if apn != "" {
		properties["apn"] = dbus.MakeVariant(apn)
	}
	if ipFamily != 0 {
		properties["ip-type"] = dbus.MakeVariant(ipFamily)
	}
	return properties
}

func bearerConnectionReady(connection domain.DataConnection) bool {
	if !connection.Connected || strings.TrimSpace(connection.Interface) == "" {
		return false
	}
	return ipConfigurationAvailable(connection.IPv4) ||
		ipConfigurationAvailable(connection.IPv6)
}

func dataConnectionMatches(
	connection domain.DataConnection,
	apn string,
	ipFamily uint32,
) bool {
	if strings.TrimSpace(apn) != "" &&
		!strings.EqualFold(strings.TrimSpace(connection.APN), strings.TrimSpace(apn)) {
		return false
	}
	return connectedFamilyMatches(connection, ipFamily)
}

func dataConnectionIsDefault(connection domain.DataConnection) bool {
	if connection.BearerType != 0 {
		return isDefaultBearerType(connection.BearerType)
	}
	return connection.APNType&domain.APNTypeDefault != 0
}

func operationErrorIsNotFound(err error) bool {
	if err == nil {
		return false
	}
	operationError, ok := domain.AsOperationError(err)
	return ok && operationError.Code == domain.ErrorNotFound
}
