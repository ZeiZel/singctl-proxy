// SystemExtensionActivator.swift
//
// Drives OSSystemExtensionManager activation of the embedded ProxyExtension
// system extension (com.singctl.proxy.netext) — the component that makes
// per-app Proxy routing possible. The native rewrite dropped the old
// activation path, so until now the app had NO way to even ask macOS for the
// extension; this restores it.
//
// Activation on a managed (MDM) Mac is expected to fail or stall unless the
// Team ID is allowlisted by the MDM's system-extension policy — the whole
// point of exposing the request and its *exact* error here is to make that
// verdict visible instead of guessing. Every transition is os_log'ed under
// subsystem com.singctl.proxy / category sysext.
//
// Developer-ID build only: the App Store SKU has no system extension.

#if !APPSTORE
import Foundation
import SystemExtensions
import os.log

/// Observable activation state machine around one
/// `OSSystemExtensionRequest.activationRequest`. Use the shared instance so
/// the AppsScreen banner and the `--activate-netext` launch flag drive the
/// same request/state.
@MainActor
final class SystemExtensionActivator: NSObject, ObservableObject {
    static let shared = SystemExtensionActivator()
    static let extensionBundleID = "com.singctl.proxy.netext"

    enum Status: Equatable {
        case idle
        case requesting
        /// macOS accepted the request and is waiting for the user to approve
        /// it in System Settings → General → Login Items & Extensions.
        case needsUserApproval
        case activated
        case activatedPendingReboot
        case failed(String)

        var label: String {
            switch self {
            case .idle: return "Not installed"
            case .requesting: return "Requesting…"
            case .needsUserApproval: return "Waiting for approval in System Settings"
            case .activated: return "Active"
            case .activatedPendingReboot: return "Active after reboot"
            case .failed(let message): return message
            }
        }
    }

    @Published private(set) var status: Status = .idle

    private let log = OSLog(subsystem: "com.singctl.proxy", category: "sysext")

    /// Submits the activation request. Safe to call repeatedly — a second
    /// submit while one is in flight is ignored.
    func activate() {
        guard status != .requesting else { return }
        status = .requesting
        os_log("submitting activation request for %{public}@", log: log, type: .info, Self.extensionBundleID)
        let request = OSSystemExtensionRequest.activationRequest(
            forExtensionWithIdentifier: Self.extensionBundleID,
            queue: .main
        )
        request.delegate = self
        OSSystemExtensionManager.shared.submitRequest(request)
    }
}

extension SystemExtensionActivator: OSSystemExtensionRequestDelegate {
    nonisolated func request(
        _ request: OSSystemExtensionRequest,
        actionForReplacingExtension existing: OSSystemExtensionProperties,
        withExtension ext: OSSystemExtensionProperties
    ) -> OSSystemExtensionRequest.ReplacementAction {
        // Same-team upgrade (e.g. new build over an older activated one):
        // always replace, matching Xcode/dev workflow expectations.
        .replace
    }

    nonisolated func requestNeedsUserApproval(_ request: OSSystemExtensionRequest) {
        Task { @MainActor in
            os_log("activation needs user approval (System Settings)", log: log, type: .info)
            status = .needsUserApproval
        }
    }

    nonisolated func request(
        _ request: OSSystemExtensionRequest,
        didFinishWithResult result: OSSystemExtensionRequest.Result
    ) {
        Task { @MainActor in
            switch result {
            case .completed:
                os_log("activation completed", log: log, type: .info)
                status = .activated
            case .willCompleteAfterReboot:
                os_log("activation completes after reboot", log: log, type: .info)
                status = .activatedPendingReboot
            @unknown default:
                os_log("activation finished with unknown result %{public}d", log: log, type: .error, result.rawValue)
                status = .failed("Unknown activation result (\(result.rawValue))")
            }
        }
    }

    nonisolated func request(_ request: OSSystemExtensionRequest, didFailWithError error: Error) {
        Task { @MainActor in
            // Surface the FULL error (domain + code + description): on MDM-
            // managed Macs the code distinguishes "blocked by policy" from
            // ordinary failures, and that distinction is the answer we're
            // after when diagnosing allowlist state.
            let ns = error as NSError
            let detail = "\(ns.localizedDescription) [\(ns.domain) \(ns.code)]"
            os_log("activation failed: %{public}@", log: log, type: .error, detail)
            status = .failed(detail)
        }
    }
}
#endif
