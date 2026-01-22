package com.ghost.testbed

import com.facebook.react.bridge.ReactApplicationContext
import com.facebook.react.bridge.ReactContextBaseJavaModule
import com.facebook.react.bridge.ReactMethod
import com.facebook.react.bridge.Promise
import mobile.Mobile

/**
 * React Native bridge for Ghost gomobile bindings.
 *
 * This module wraps the gomobile-generated Java classes from ghost.aar
 * and exposes them to JavaScript through React Native's native module system.
 */
class GhostModule(reactContext: ReactApplicationContext) : ReactContextBaseJavaModule(reactContext) {

    private var client: mobile.GhostClient? = null

    override fun getName(): String = "GhostModule"

    @ReactMethod
    fun newClient(stunServers: String, promise: Promise) {
        try {
            client?.close()
            client = Mobile.newClient(stunServers)
            promise.resolve(null)
        } catch (e: Exception) {
            promise.reject("GHOST_ERROR", e.message, e)
        }
    }

    @ReactMethod
    fun close(promise: Promise) {
        try {
            client?.close()
            client = null
            promise.resolve(null)
        } catch (e: Exception) {
            promise.reject("GHOST_ERROR", e.message, e)
        }
    }

    @ReactMethod
    fun generateWireGuardKey(promise: Promise) {
        try {
            val result = client?.generateWireGuardKey() ?: throw IllegalStateException("Client not initialized")
            promise.resolve(result)
        } catch (e: Exception) {
            promise.reject("GHOST_ERROR", e.message, e)
        }
    }

    @ReactMethod(isBlockingSynchronousMethod = true)
    fun getPublicKey(): String {
        return try {
            client?.getPublicKey() ?: ""
        } catch (e: Exception) {
            ""
        }
    }

    @ReactMethod
    fun startGathering(promise: Promise) {
        try {
            val result = client?.startGathering() ?: throw IllegalStateException("Client not initialized")
            promise.resolve(result)
        } catch (e: Exception) {
            promise.reject("GHOST_ERROR", e.message, e)
        }
    }

    @ReactMethod(isBlockingSynchronousMethod = true)
    fun getLocalCredentials(): String {
        return try {
            client?.getLocalCredentials() ?: "{}"
        } catch (e: Exception) {
            "{\"error\": \"${e.message}\"}"
        }
    }

    @ReactMethod(isBlockingSynchronousMethod = true)
    fun getLocalCandidatesJSON(): String {
        return try {
            client?.getLocalCandidatesJSON() ?: "[]"
        } catch (e: Exception) {
            "[]"
        }
    }

    @ReactMethod
    fun setRemoteCredentials(json: String, promise: Promise) {
        try {
            val result = client?.setRemoteCredentials(json) ?: throw IllegalStateException("Client not initialized")
            promise.resolve(result)
        } catch (e: Exception) {
            promise.reject("GHOST_ERROR", e.message, e)
        }
    }

    @ReactMethod
    fun addRemoteCandidate(json: String, promise: Promise) {
        try {
            val result = client?.addRemoteCandidate(json) ?: throw IllegalStateException("Client not initialized")
            promise.resolve(result)
        } catch (e: Exception) {
            promise.reject("GHOST_ERROR", e.message, e)
        }
    }

    @ReactMethod
    fun connect(isControlling: Boolean, promise: Promise) {
        try {
            val result = client?.connect(isControlling) ?: throw IllegalStateException("Client not initialized")
            promise.resolve(result)
        } catch (e: Exception) {
            promise.reject("GHOST_ERROR", e.message, e)
        }
    }

    @ReactMethod(isBlockingSynchronousMethod = true)
    fun getConnectionState(): String {
        return try {
            client?.getConnectionState() ?: "{\"iceState\":\"new\",\"tunnelState\":\"inactive\",\"isConnected\":false,\"isTunnelActive\":false}"
        } catch (e: Exception) {
            "{\"error\": \"${e.message}\"}"
        }
    }

    @ReactMethod
    fun setPeerPublicKey(base64Key: String, promise: Promise) {
        try {
            val result = client?.setPeerPublicKey(base64Key) ?: throw IllegalStateException("Client not initialized")
            promise.resolve(result)
        } catch (e: Exception) {
            promise.reject("GHOST_ERROR", e.message, e)
        }
    }

    @ReactMethod
    fun setLocalIP(cidr: String, promise: Promise) {
        try {
            val result = client?.setLocalIP(cidr) ?: throw IllegalStateException("Client not initialized")
            promise.resolve(result)
        } catch (e: Exception) {
            promise.reject("GHOST_ERROR", e.message, e)
        }
    }

    @ReactMethod
    fun startTunnel(promise: Promise) {
        try {
            val result = client?.startTunnel() ?: throw IllegalStateException("Client not initialized")
            promise.resolve(result)
        } catch (e: Exception) {
            promise.reject("GHOST_ERROR", e.message, e)
        }
    }

    @ReactMethod
    fun httpGet(url: String, promise: Promise) {
        try {
            val result = client?.httpGet(url) ?: throw IllegalStateException("Client not initialized")
            promise.resolve(result)
        } catch (e: Exception) {
            promise.reject("GHOST_ERROR", e.message, e)
        }
    }

    @ReactMethod
    fun httpPost(url: String, contentType: String, body: String, promise: Promise) {
        try {
            val result = client?.httpPost(url, contentType, body) ?: throw IllegalStateException("Client not initialized")
            promise.resolve(result)
        } catch (e: Exception) {
            promise.reject("GHOST_ERROR", e.message, e)
        }
    }

    @ReactMethod(isBlockingSynchronousMethod = true)
    fun getTunnelStats(): String {
        return try {
            client?.getTunnelStats() ?: "{\"isActive\":false,\"bytesSent\":0,\"bytesReceived\":0}"
        } catch (e: Exception) {
            "{\"error\": \"${e.message}\"}"
        }
    }

    @ReactMethod(isBlockingSynchronousMethod = true)
    fun getSignalingData(): String {
        return try {
            client?.getSignalingData() ?: ""
        } catch (e: Exception) {
            "{\"error\": \"${e.message}\"}"
        }
    }

    @ReactMethod
    fun setSignalingData(json: String, promise: Promise) {
        try {
            val result = client?.setSignalingData(json) ?: throw IllegalStateException("Client not initialized")
            promise.resolve(result)
        } catch (e: Exception) {
            promise.reject("GHOST_ERROR", e.message, e)
        }
    }
}
