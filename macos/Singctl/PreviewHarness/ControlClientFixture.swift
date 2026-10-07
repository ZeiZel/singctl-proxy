// Hermetic transport fixture for ControlClient endpoint/lifecycle behavior.
// Compile this file with -D CONTROL_CLIENT_FIXTURE alongside Models.swift,
// InstanceDiscovery.swift, and ControlClient.swift. It never opens a socket.
#if CONTROL_CLIENT_FIXTURE
import Foundation

@main
struct ControlClientFixture {
    static func main() async throws {
        let a = InstanceDiscovery.Endpoint(controlSocket: "/tmp/a", clashAPIAddr: "",
            clashSecret: "", pid: 7, mode: "off", startedAt: "a")
        let b = InstanceDiscovery.Endpoint(controlSocket: "/tmp/b", clashAPIAddr: "",
            clashSecret: "", pid: 7, mode: "off", startedAt: "b")
        let status = #"{"pid":7,"mode":"off","started_at":"b"}"#
        var endpoints: [InstanceDiscovery.Endpoint?] = [a, b]
        var calls = 0
        let client = ControlClient(endpointProvider: {
            defer { if !endpoints.isEmpty { endpoints.removeFirst() } }
            return endpoints.isEmpty ? b : endpoints[0]
        }, transportOverride: { endpoint, verb, _ in
            calls += 1
            if calls == 1 { throw ControlClientError.connectFailed("rotated") }
            precondition(endpoint.controlSocket == "/tmp/b" && verb == "STATUS")
            return status
        })
        let fetched = try await client.status()
        precondition(fetched.pid == 7)
        precondition(calls == 2)

        var available = false
        let reconnect = ControlClient(endpointProvider: {
            defer { available = true }
            return available ? b : nil
        }, transportOverride: { _, verb, _ in
            precondition(verb == "STATUS")
            return status
        })
        do { _ = try await reconnect.status(); preconditionFailure("offline status unexpectedly succeeded") }
        catch { }
        let recovered = try await reconnect.status()
        precondition(recovered.pid == 7)

        var mutationCalls = 0
        var mutationEndpointCalls = 0
        let mutation = ControlClient(endpointProvider: {
            mutationEndpointCalls += 1
            return mutationEndpointCalls == 1 ? a : b
        }, transportOverride: { _, _, _ in
            mutationCalls += 1
            throw ControlClientError.connectFailed("lost reply")
        })
        do { try await mutation.setMode("proxy"); preconditionFailure("mutation unexpectedly succeeded") }
        catch { }
        precondition(mutationCalls == 1 && mutationEndpointCalls == 1)
        print("ControlClient fixture passed")
    }
}
#endif
