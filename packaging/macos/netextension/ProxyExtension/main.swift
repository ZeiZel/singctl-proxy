// Entry point for the system extension executable. Unlike an .appex (which the
// system instantiates via NSExtensionPrincipalClass), a NetworkExtension system
// extension is a standalone binary: it must call startSystemExtensionMode(),
// which reads NEProviderClasses from Info.plist and instantiates the mapped
// provider (TransparentProxyProvider) when the system starts the proxy. Without
// this, linking fails with an undefined "_main" symbol.
import Foundation
import NetworkExtension

autoreleasepool {
    NEProvider.startSystemExtensionMode()
}

dispatchMain()
