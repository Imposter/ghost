package com.ghost.testbed

import androidx.test.ext.junit.runners.AndroidJUnit4
import mobile.Mobile
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

/**
 * Instrumented tests for ghost-go mobile bindings.
 * These tests run on an Android device/emulator.
 *
 * Run with: ./gradlew connectedAndroidTest
 */
@RunWith(AndroidJUnit4::class)
class GhostClientTest {

    private lateinit var client: mobile.GhostClient

    @Before
    fun setUp() {
        client = Mobile.newClient("")
    }

    @After
    fun tearDown() {
        client.close()
    }

    @Test
    fun testClientCreation() {
        // Client should be created without errors
        assertNotNull(client)
    }

    @Test
    fun testKeyGeneration() {
        val keyPairJson = client.generateWireGuardKey()
        assertFalse("Key generation should not return error", keyPairJson.contains("error"))

        val keyPair = JSONObject(keyPairJson)
        assertTrue("Should have privateKey", keyPair.has("privateKey"))
        assertTrue("Should have publicKey", keyPair.has("publicKey"))

        val privateKey = keyPair.getString("privateKey")
        val publicKey = keyPair.getString("publicKey")

        assertEquals("Private key should be 44 chars (base64)", 44, privateKey.length)
        assertEquals("Public key should be 44 chars (base64)", 44, publicKey.length)
    }

    @Test
    fun testGetPublicKey() {
        // Generate keys first
        client.generateWireGuardKey()

        val pubKey = client.publicKey
        assertNotNull(pubKey)
        assertFalse("Public key should not be empty", pubKey.isEmpty())
        assertEquals("Public key should be 44 chars", 44, pubKey.length)
    }

    @Test
    fun testConnectionState() {
        val stateJson = client.connectionState
        assertFalse("Connection state should not be empty", stateJson.isEmpty())

        val state = JSONObject(stateJson)
        assertTrue("Should have iceState", state.has("iceState"))
        assertTrue("Should have tunnelState", state.has("tunnelState"))
        assertTrue("Should have isConnected", state.has("isConnected"))

        assertEquals("Initial ICE state should be 'new'", "new", state.getString("iceState"))
        assertEquals("Initial tunnel state should be 'inactive'", "inactive", state.getString("tunnelState"))
        assertFalse("Should not be connected initially", state.getBoolean("isConnected"))
    }

    @Test
    fun testSetPeerPublicKey_Invalid() {
        val result = client.setPeerPublicKey("not-valid-base64")
        assertTrue("Should return error for invalid key", result.contains("error"))
    }

    @Test
    fun testSetPeerPublicKey_Valid() {
        // Generate keys to get a valid public key
        client.generateWireGuardKey()
        val pubKey = client.publicKey

        // Use the same key as peer key for testing
        val result = client.setPeerPublicKey(pubKey)
        assertFalse("Should not return error for valid key", result.contains("error"))
    }

    @Test
    fun testSetLocalIP_Valid() {
        val result = client.setLocalIP("10.0.0.5/24")
        assertTrue("setLocalIP should return empty on success", result.isEmpty())
    }

    @Test
    fun testSetLocalIP_Invalid() {
        val result = client.setLocalIP("not-an-ip")
        assertTrue("Should return error for invalid IP", result.contains("error"))
    }

    @Test
    fun testHTTPGet_BeforeTunnel() {
        val result = client.httpGet("http://10.0.0.1:8080/test")

        val httpResult = JSONObject(result)
        assertFalse("HTTP request should fail before tunnel", httpResult.getBoolean("success"))
        assertTrue("Should have error message", httpResult.has("error"))
    }

    @Test
    fun testTunnelStats_BeforeTunnel() {
        val statsJson = client.tunnelStats

        val stats = JSONObject(statsJson)
        assertFalse("Tunnel should be inactive", stats.getBoolean("isActive"))
        assertEquals("Bytes sent should be 0", 0, stats.getLong("bytesSent"))
        assertEquals("Bytes received should be 0", 0, stats.getLong("bytesReceived"))
    }

    @Test
    fun testStartTunnel_Prerequisites() {
        // Try to start tunnel without ICE connection
        val result = client.startTunnel()
        assertTrue("Should return error without ICE connection", result.contains("error"))
    }

    @Test
    fun testConnect_BeforeGathering() {
        val result = client.connect(true)
        assertTrue("Should return error before gathering", result.contains("error"))
    }

    @Test
    fun testGetSignalingData_BeforeGathering() {
        val result = client.signalingData
        assertTrue("Should return error before gathering", result.contains("error"))
    }

    @Test
    fun testSetRemoteCredentials_BeforeGathering() {
        val result = client.setRemoteCredentials("""{"ufrag": "test", "pwd": "password"}""")
        assertTrue("Should return error before gathering", result.contains("error"))
    }

    @Test
    fun testCloseIdempotent() {
        // Close should be callable multiple times without error
        client.close()
        client.close()
        // If we get here without exception, test passes
    }
}
