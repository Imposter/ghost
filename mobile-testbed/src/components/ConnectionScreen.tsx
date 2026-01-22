import React, { useState, useEffect } from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  ScrollView,
  TextInput,
  Alert,
  ActivityIndicator,
} from 'react-native';
import { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { RootStackParamList } from '../App';
import { useGhostClient, SignalingData, Candidate } from '../hooks/useGhostClient';

type ConnectionScreenProps = {
  navigation: NativeStackNavigationProp<RootStackParamList, 'Connection'>;
};

export default function ConnectionScreen({ navigation }: ConnectionScreenProps) {
  const {
    status,
    candidates,
    connectionState,
    publicKey,
    error,
    isInitialized,
    initialize,
    close,
    generateKeys,
    startGathering,
    pollCandidates,
    getSignalingData,
    setSignalingData,
    connect,
    startTunnel,
  } = useGhostClient();

  const [peerDataInput, setPeerDataInput] = useState('');
  const [localSignalingData, setLocalSignalingData] = useState<SignalingData | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [step, setStep] = useState<'init' | 'gathering' | 'exchange' | 'connecting' | 'connected'>('init');

  // Initialize client on mount
  useEffect(() => {
    const init = async () => {
      setIsLoading(true);
      await initialize('stun:stun.l.google.com:19302,stun:stun1.l.google.com:19302');
      setIsLoading(false);
    };
    init();

    return () => {
      close();
    };
  }, [initialize, close]);

  // Poll candidates while gathering
  useEffect(() => {
    if (status === 'gathering') {
      const interval = setInterval(() => {
        pollCandidates();
      }, 500);
      return () => clearInterval(interval);
    }
  }, [status, pollCandidates]);

  // Update local signaling data when candidates change
  useEffect(() => {
    if (candidates.length > 0 && status === 'gathering') {
      const data = getSignalingData();
      if (data) {
        setLocalSignalingData(data);
      }
    }
  }, [candidates, status, getSignalingData]);

  const handleStartGathering = async () => {
    setIsLoading(true);
    const keyResult = await generateKeys();
    if (!keyResult) {
      setIsLoading(false);
      return;
    }

    const success = await startGathering();
    setIsLoading(false);
    if (success) {
      setStep('gathering');
    }
  };

  const handleSetPeerData = async () => {
    if (!peerDataInput.trim()) {
      Alert.alert('Error', 'Please enter peer signaling data');
      return;
    }

    try {
      const peerData: SignalingData = JSON.parse(peerDataInput);
      setIsLoading(true);
      const success = await setSignalingData(peerData);
      setIsLoading(false);

      if (success) {
        setStep('exchange');
        Alert.alert('Success', 'Peer data set successfully. Ready to connect.');
      }
    } catch (err) {
      Alert.alert('Error', 'Invalid JSON format');
    }
  };

  const handleConnect = async () => {
    setIsLoading(true);
    setStep('connecting');

    // Mobile is controlled (not controlling)
    const iceSuccess = await connect(false);
    if (!iceSuccess) {
      setIsLoading(false);
      setStep('exchange');
      return;
    }

    // Start WireGuard tunnel
    const tunnelSuccess = await startTunnel();
    setIsLoading(false);

    if (tunnelSuccess) {
      setStep('connected');
      Alert.alert('Connected!', 'Tunnel established successfully. You can now test connectivity.', [
        { text: 'Test Now', onPress: () => navigation.navigate('Test') },
        { text: 'OK' },
      ]);
    } else {
      setStep('exchange');
    }
  };

  const copySignalingData = () => {
    if (localSignalingData) {
      // In a real app, use Clipboard.setString
      Alert.alert('Signaling Data', JSON.stringify(localSignalingData, null, 2));
    }
  };

  const renderCandidates = () => {
    if (candidates.length === 0) return null;

    return (
      <View style={styles.candidateSection}>
        <Text style={styles.sectionTitle}>Local Candidates ({candidates.length})</Text>
        {candidates.map((c: Candidate, i: number) => (
          <View key={i} style={styles.candidateItem}>
            <Text style={styles.candidateType}>{c.type}</Text>
            <Text style={styles.candidateAddr}>{c.address}:{c.port}</Text>
          </View>
        ))}
      </View>
    );
  };

  const renderConnectionState = () => {
    if (!connectionState) return null;

    return (
      <View style={styles.stateSection}>
        <Text style={styles.sectionTitle}>Connection State</Text>
        <View style={styles.stateRow}>
          <Text style={styles.stateLabel}>ICE:</Text>
          <Text style={styles.stateValue}>{connectionState.iceState}</Text>
        </View>
        <View style={styles.stateRow}>
          <Text style={styles.stateLabel}>Tunnel:</Text>
          <Text style={styles.stateValue}>{connectionState.tunnelState}</Text>
        </View>
        {connectionState.localIP && (
          <View style={styles.stateRow}>
            <Text style={styles.stateLabel}>Local IP:</Text>
            <Text style={styles.stateValue}>{connectionState.localIP}</Text>
          </View>
        )}
        {connectionState.peerIP && (
          <View style={styles.stateRow}>
            <Text style={styles.stateLabel}>Peer IP:</Text>
            <Text style={styles.stateValue}>{connectionState.peerIP}</Text>
          </View>
        )}
      </View>
    );
  };

  return (
    <ScrollView style={styles.container} contentContainerStyle={styles.content}>
      {/* Status Banner */}
      <View style={[styles.statusBanner, styles[`status_${status}`]]}>
        <Text style={styles.statusText}>
          {status.charAt(0).toUpperCase() + status.slice(1)}
        </Text>
        {isLoading && <ActivityIndicator color="#fff" style={styles.spinner} />}
      </View>

      {/* Error Display */}
      {error && (
        <View style={styles.errorBox}>
          <Text style={styles.errorText}>{error}</Text>
        </View>
      )}

      {/* Step 1: Initialize and Start Gathering */}
      {step === 'init' && (
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>Step 1: Start Gathering</Text>
          <Text style={styles.helpText}>
            Generate WireGuard keys and gather ICE candidates for NAT traversal.
          </Text>
          <TouchableOpacity
            style={[styles.button, !isInitialized && styles.buttonDisabled]}
            onPress={handleStartGathering}
            disabled={!isInitialized || isLoading}
          >
            <Text style={styles.buttonText}>
              {isLoading ? 'Starting...' : 'Start Gathering'}
            </Text>
          </TouchableOpacity>
        </View>
      )}

      {/* Step 2: Show Local Data */}
      {(step === 'gathering' || step === 'exchange') && (
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>Step 2: Share Your Data</Text>
          {publicKey && (
            <View style={styles.keyBox}>
              <Text style={styles.keyLabel}>Public Key:</Text>
              <Text style={styles.keyValue} numberOfLines={1}>{publicKey}</Text>
            </View>
          )}
          {renderCandidates()}
          {localSignalingData && (
            <TouchableOpacity style={styles.secondaryButton} onPress={copySignalingData}>
              <Text style={styles.secondaryButtonText}>View Signaling Data</Text>
            </TouchableOpacity>
          )}
        </View>
      )}

      {/* Step 3: Enter Peer Data */}
      {(step === 'gathering' || step === 'exchange') && candidates.length > 0 && (
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>Step 3: Enter Peer Data</Text>
          <Text style={styles.helpText}>
            Paste the signaling data from the desktop server.
          </Text>
          <TextInput
            style={styles.input}
            multiline
            numberOfLines={4}
            placeholder='{"ufrag":"...", "pwd":"...", "publicKey":"...", "candidates":[...]}'
            placeholderTextColor="#666"
            value={peerDataInput}
            onChangeText={setPeerDataInput}
          />
          <TouchableOpacity
            style={styles.button}
            onPress={handleSetPeerData}
            disabled={isLoading}
          >
            <Text style={styles.buttonText}>Set Peer Data</Text>
          </TouchableOpacity>
        </View>
      )}

      {/* Step 4: Connect */}
      {step === 'exchange' && (
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>Step 4: Connect</Text>
          <Text style={styles.helpText}>
            Establish ICE connection and start WireGuard tunnel.
          </Text>
          <TouchableOpacity
            style={styles.button}
            onPress={handleConnect}
            disabled={isLoading}
          >
            <Text style={styles.buttonText}>
              {isLoading ? 'Connecting...' : 'Connect'}
            </Text>
          </TouchableOpacity>
        </View>
      )}

      {/* Connection State */}
      {renderConnectionState()}

      {/* Connected State */}
      {step === 'connected' && (
        <View style={styles.section}>
          <View style={styles.successBox}>
            <Text style={styles.successText}>✓ Tunnel Active</Text>
          </View>
          <TouchableOpacity
            style={styles.button}
            onPress={() => navigation.navigate('Test')}
          >
            <Text style={styles.buttonText}>Test Connectivity</Text>
          </TouchableOpacity>
        </View>
      )}
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#1a1a1a',
  },
  content: {
    padding: 16,
  },
  statusBanner: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    padding: 12,
    borderRadius: 8,
    marginBottom: 16,
  },
  status_disconnected: {
    backgroundColor: '#666',
  },
  status_gathering: {
    backgroundColor: '#2196F3',
  },
  status_connecting: {
    backgroundColor: '#FF9800',
  },
  status_connected: {
    backgroundColor: '#4CAF50',
  },
  status_error: {
    backgroundColor: '#F44336',
  },
  statusText: {
    color: '#fff',
    fontSize: 16,
    fontWeight: '600',
  },
  spinner: {
    marginLeft: 8,
  },
  errorBox: {
    backgroundColor: '#F44336',
    padding: 12,
    borderRadius: 8,
    marginBottom: 16,
  },
  errorText: {
    color: '#fff',
    fontSize: 14,
  },
  section: {
    backgroundColor: '#2a2a2a',
    borderRadius: 12,
    padding: 16,
    marginBottom: 16,
  },
  sectionTitle: {
    fontSize: 18,
    fontWeight: '600',
    color: '#fff',
    marginBottom: 8,
  },
  helpText: {
    fontSize: 14,
    color: '#888',
    marginBottom: 12,
  },
  button: {
    backgroundColor: '#4CAF50',
    borderRadius: 8,
    padding: 14,
    alignItems: 'center',
  },
  buttonDisabled: {
    backgroundColor: '#444',
  },
  buttonText: {
    color: '#fff',
    fontSize: 16,
    fontWeight: '600',
  },
  secondaryButton: {
    backgroundColor: '#333',
    borderRadius: 8,
    padding: 12,
    alignItems: 'center',
    borderWidth: 1,
    borderColor: '#4CAF50',
    marginTop: 12,
  },
  secondaryButtonText: {
    color: '#4CAF50',
    fontSize: 14,
    fontWeight: '600',
  },
  keyBox: {
    backgroundColor: '#333',
    borderRadius: 8,
    padding: 12,
    marginBottom: 12,
  },
  keyLabel: {
    fontSize: 12,
    color: '#888',
    marginBottom: 4,
  },
  keyValue: {
    fontSize: 14,
    color: '#4CAF50',
    fontFamily: 'monospace',
  },
  candidateSection: {
    marginTop: 12,
  },
  candidateItem: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    backgroundColor: '#333',
    padding: 8,
    borderRadius: 4,
    marginTop: 4,
  },
  candidateType: {
    color: '#4CAF50',
    fontSize: 12,
    fontWeight: '600',
  },
  candidateAddr: {
    color: '#ccc',
    fontSize: 12,
    fontFamily: 'monospace',
  },
  input: {
    backgroundColor: '#333',
    borderRadius: 8,
    padding: 12,
    color: '#fff',
    fontSize: 14,
    fontFamily: 'monospace',
    minHeight: 100,
    textAlignVertical: 'top',
    marginBottom: 12,
  },
  stateSection: {
    backgroundColor: '#2a2a2a',
    borderRadius: 12,
    padding: 16,
    marginBottom: 16,
  },
  stateRow: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    marginTop: 8,
  },
  stateLabel: {
    color: '#888',
    fontSize: 14,
  },
  stateValue: {
    color: '#fff',
    fontSize: 14,
    fontWeight: '500',
  },
  successBox: {
    backgroundColor: '#4CAF50',
    borderRadius: 8,
    padding: 16,
    alignItems: 'center',
    marginBottom: 16,
  },
  successText: {
    color: '#fff',
    fontSize: 18,
    fontWeight: '600',
  },
});
