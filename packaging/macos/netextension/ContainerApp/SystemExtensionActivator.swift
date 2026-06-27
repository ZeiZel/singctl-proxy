// SystemExtensionActivator.swift — container-app side (SCAFFOLD).
//
// The System Extension cannot install or configure itself. The container app:
//   1) requests activation via OSSystemExtensionManager (user approves once in
//      System Settings → Login Items & Extensions),
//   2) creates/loads a NETransparentProxyManager and writes the providerConfiguration
//      (targets + SOCKS host:port) that the provider reads in startProxy.
//
// STATUS: skeleton. Wire this into the singctl macOS helper app; handle the
// approval-required and replacement states properly before shipping.

import Foundation
import NetworkExtension
import SystemExtensions
import os.log

final class SystemExtensionActivator: NSObject, OSSystemExtensionRequestDelegate {
    private let log = OSLog(subsystem: "com.singctl.proxy", category: "activator")
    private let extensionIdentifier = "com.singctl.proxy.netext"

    /// Result of an activation request, surfaced to the caller so it only
    /// configures the manager once the extension is actually usable.
    enum ActivationResult {
        case success                 // .completed
        case rebootRequired          // .willCompleteAfterReboot
    }

    /// Errors specific to activation (beyond the OS-provided ones).
    enum ActivationError: LocalizedError {
        case needsApproval
        var errorDescription: String? {
            switch self {
            case .needsApproval:
                return "Расширение требует одобрения: System Settings → General → " +
                    "Login Items & Extensions → Network Extensions."
            }
        }
    }

    private var onActivation: ((Result<ActivationResult, Error>) -> Void)?

    /// Step 1: ask the OS to (re)activate the system extension. `completion` is
    /// called on the main queue once the request finishes (or fails); configure()
    /// should only run on `.success`.
    func activate(completion: ((Result<ActivationResult, Error>) -> Void)? = nil) {
        onActivation = completion
        let req = OSSystemExtensionRequest.activationRequest(
            forExtensionWithIdentifier: extensionIdentifier, queue: .main)
        req.delegate = self
        OSSystemExtensionManager.shared.submitRequest(req)
    }

    private func finishActivation(_ result: Result<ActivationResult, Error>) {
        let cb = onActivation
        onActivation = nil
        cb?(result)
    }

    /// Step 2: configure the transparent-proxy manager. `targets` are the bundle
    /// IDs singctl wants captured (e.g. the leaky-editors set); socks is the
    /// local proxy. Call again to update targets live.
    func configure(targets: [String], socksHost: String = "127.0.0.1", socksPort: Int = 1080,
                  completion: @escaping (Error?) -> Void) {
        NETransparentProxyManager.loadAllFromPreferences { managers, error in
            if let error = error { completion(error); return }
            let manager = managers?.first ?? NETransparentProxyManager()

            let proto = NETunnelProviderProtocol()
            proto.providerBundleIdentifier = self.extensionIdentifier
            proto.serverAddress = "singctl" // cosmetic; required to be non-empty
            proto.providerConfiguration = [
                "targets": targets,
                "socksHost": socksHost,
                "socksPort": socksPort,
            ]

            manager.localizedDescription = "singctl per-app proxy"
            manager.protocolConfiguration = proto
            manager.isEnabled = !targets.isEmpty

            manager.saveToPreferences { error in completion(error) }
        }
    }

    /// Remove the proxy configuration (e.g. user disabled per-app proxying).
    func deactivate() {
        let req = OSSystemExtensionRequest.deactivationRequest(
            forExtensionWithIdentifier: extensionIdentifier, queue: .main)
        req.delegate = self
        OSSystemExtensionManager.shared.submitRequest(req)
    }

    // MARK: OSSystemExtensionRequestDelegate

    func request(_ request: OSSystemExtensionRequest,
                actionForReplacingExtension existing: OSSystemExtensionProperties,
                withExtension ext: OSSystemExtensionProperties) -> OSSystemExtensionRequest.ReplacementAction {
        .replace // always take the newly-shipped build
    }

    func requestNeedsUserApproval(_ request: OSSystemExtensionRequest) {
        os_log("system extension needs user approval in System Settings", log: log, type: .info)
        // Not terminal — didFinishWithResult/didFailWithError still arrives after
        // the user acts. Surface guidance; the caller can show UI on this error.
        finishActivation(.failure(ActivationError.needsApproval))
    }

    func request(_ request: OSSystemExtensionRequest,
                didFinishWithResult result: OSSystemExtensionRequest.Result) {
        os_log("activation result=%d", log: log, type: .info, result.rawValue)
        switch result {
        case .completed:
            finishActivation(.success(.success))
        case .willCompleteAfterReboot:
            finishActivation(.success(.rebootRequired))
        @unknown default:
            finishActivation(.success(.success))
        }
    }

    func request(_ request: OSSystemExtensionRequest, didFailWithError error: Error) {
        os_log("activation failed: %{public}@", log: log, type: .error, error.localizedDescription)
        finishActivation(.failure(error))
    }
}
