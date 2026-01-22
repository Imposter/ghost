import XCTest
import Mobile

/// Tests for ghost-go mobile bindings on iOS.
/// Run with: xcodebuild test -scheme GhostTestbed -destination 'platform=iOS Simulator,name=iPhone 15'
final class GhostClientTests: XCTestCase {

    var client: MobileGhostClient!

    override func setUpWithError() throws {
        var error: NSError?
        client = MobileNewClient("", &error)
        XCTAssertNil(error, "Client creation should not error")
        XCTAssertNotNil(client, "Client should be created")
    }

    override func tearDownWithError() throws {
        try client?.close()
    }

    func testClientCreation() {
        XCTAssertNotNil(client)
    }

    func testKeyGeneration() {
        let keyPairJson = client.generateWireGuardKey()
        XCTAssertFalse(keyPairJson.contains("error"), "Key generation should not return error")

        // Parse JSON to verify structure
        guard let data = keyPairJson.data(using: .utf8),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: String] else {
            XCTFail("Failed to parse key pair JSON")
            return
        }

        XCTAssertNotNil(json["privateKey"], "Should have privateKey")
        XCTAssertNotNil(json["publicKey"], "Should have publicKey")

        XCTAssertEqual(json["privateKey"]?.count, 44, "Private key should be 44 chars (base64)")
        XCTAssertEqual(json["publicKey"]?.count, 44, "Public key should be 44 chars (base64)")
    }

    func testGetPublicKey() {
        // Generate keys first
        _ = client.generateWireGuardKey()

        let pubKey = client.getPublicKey()
        XCTAssertNotNil(pubKey)
        XCTAssertFalse(pubKey.isEmpty, "Public key should not be empty")
        XCTAssertEqual(pubKey.count, 44, "Public key should be 44 chars")
    }

    func testConnectionState() {
        let stateJson = client.getConnectionState()
        XCTAssertFalse(stateJson.isEmpty, "Connection state should not be empty")

        guard let data = stateJson.data(using: .utf8),
              let state = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            XCTFail("Failed to parse connection state JSON")
            return
        }

        XCTAssertNotNil(state["iceState"], "Should have iceState")
        XCTAssertNotNil(state["tunnelState"], "Should have tunnelState")
        XCTAssertNotNil(state["isConnected"], "Should have isConnected")

        XCTAssertEqual(state["iceState"] as? String, "new", "Initial ICE state should be 'new'")
        XCTAssertEqual(state["tunnelState"] as? String, "inactive", "Initial tunnel state should be 'inactive'")
        XCTAssertEqual(state["isConnected"] as? Bool, false, "Should not be connected initially")
    }

    func testSetPeerPublicKeyInvalid() {
        let result = client.setPeerPublicKey("not-valid-base64")
        XCTAssertTrue(result.contains("error"), "Should return error for invalid key")
    }

    func testSetPeerPublicKeyValid() {
        // Generate keys to get a valid public key
        _ = client.generateWireGuardKey()
        let pubKey = client.getPublicKey()

        // Use the same key as peer key for testing
        let result = client.setPeerPublicKey(pubKey)
        XCTAssertFalse(result.contains("error"), "Should not return error for valid key")
    }

    func testSetLocalIPValid() {
        let result = client.setLocalIP("10.0.0.5/24")
        XCTAssertTrue(result.isEmpty, "setLocalIP should return empty on success")
    }

    func testSetLocalIPInvalid() {
        let result = client.setLocalIP("not-an-ip")
        XCTAssertTrue(result.contains("error"), "Should return error for invalid IP")
    }

    func testHTTPGetBeforeTunnel() {
        let result = client.httpGet("http://10.0.0.1:8080/test")

        guard let data = result.data(using: .utf8),
              let httpResult = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            XCTFail("Failed to parse HTTP result JSON")
            return
        }

        XCTAssertEqual(httpResult["success"] as? Bool, false, "HTTP request should fail before tunnel")
        XCTAssertNotNil(httpResult["error"], "Should have error message")
    }

    func testTunnelStatsBeforeTunnel() {
        let statsJson = client.getTunnelStats()

        guard let data = statsJson.data(using: .utf8),
              let stats = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            XCTFail("Failed to parse tunnel stats JSON")
            return
        }

        XCTAssertEqual(stats["isActive"] as? Bool, false, "Tunnel should be inactive")
        XCTAssertEqual(stats["bytesSent"] as? Int, 0, "Bytes sent should be 0")
        XCTAssertEqual(stats["bytesReceived"] as? Int, 0, "Bytes received should be 0")
    }

    func testStartTunnelPrerequisites() {
        // Try to start tunnel without ICE connection
        let result = client.startTunnel()
        XCTAssertTrue(result.contains("error"), "Should return error without ICE connection")
    }

    func testConnectBeforeGathering() {
        let result = client.connect(true)
        XCTAssertTrue(result.contains("error"), "Should return error before gathering")
    }

    func testGetSignalingDataBeforeGathering() {
        let result = client.getSignalingData()
        XCTAssertTrue(result.contains("error"), "Should return error before gathering")
    }

    func testSetRemoteCredentialsBeforeGathering() {
        let result = client.setRemoteCredentials("{\"ufrag\": \"test\", \"pwd\": \"password\"}")
        XCTAssertTrue(result.contains("error"), "Should return error before gathering")
    }

    func testCloseIdempotent() throws {
        // Close should be callable multiple times without error
        try client.close()
        try client.close()
        // If we get here without exception, test passes
    }
}
