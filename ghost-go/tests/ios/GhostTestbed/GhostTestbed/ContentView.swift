import SwiftUI
import Mobile

struct ContentView: View {
    @State private var status = "Initializing..."
    @State private var publicKey = ""

    var body: some View {
        VStack(spacing: 20) {
            Text("Ghost-GO Testbed")
                .font(.title)

            Text(status)
                .foregroundColor(.secondary)

            if !publicKey.isEmpty {
                Text("Public Key:")
                    .font(.headline)
                Text(publicKey)
                    .font(.caption)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }

            Button("Test Client") {
                testClient()
            }
            .buttonStyle(.borderedProminent)
        }
        .padding()
        .onAppear {
            testClient()
        }
    }

    func testClient() {
        do {
            // Create client
            var error: NSError?
            guard let client = MobileNewClient("", &error) else {
                status = "Error creating client: \(error?.localizedDescription ?? "unknown")"
                return
            }

            // Generate keys
            let keyPairJson = client.generateWireGuardKey()
            if keyPairJson.contains("error") {
                status = "Key generation error: \(keyPairJson)"
                return
            }

            // Get public key
            publicKey = client.getPublicKey()

            // Get connection state
            let stateJson = client.getConnectionState()

            // Close client
            try client.close()

            status = "Client test successful!"

        } catch {
            status = "Error: \(error.localizedDescription)"
        }
    }
}

#Preview {
    ContentView()
}
