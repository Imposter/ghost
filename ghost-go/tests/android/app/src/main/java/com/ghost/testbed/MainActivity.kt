package com.ghost.testbed

import android.os.Bundle
import android.util.Log
import androidx.appcompat.app.AppCompatActivity
import mobile.Mobile

/**
 * Main activity for Ghost-GO testbed.
 * This is a minimal test app to verify gomobile bindings work on Android.
 */
class MainActivity : AppCompatActivity() {

    companion object {
        private const val TAG = "GhostTestbed"
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        Log.i(TAG, "Ghost Testbed starting...")

        // Basic smoke test: create a client and generate keys
        try {
            val client = Mobile.newClient("")
            Log.i(TAG, "Client created successfully")

            val keyPair = client.generateWireGuardKey()
            Log.i(TAG, "Keys generated: $keyPair")

            val pubKey = client.publicKey
            Log.i(TAG, "Public key: $pubKey")

            val state = client.connectionState
            Log.i(TAG, "Connection state: $state")

            client.close()
            Log.i(TAG, "Client closed successfully")

        } catch (e: Exception) {
            Log.e(TAG, "Error: ${e.message}", e)
        }
    }
}
