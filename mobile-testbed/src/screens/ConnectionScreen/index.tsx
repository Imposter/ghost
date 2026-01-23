import React, { useState, useEffect, useRef } from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  ScrollView,
  Alert,
} from 'react-native';
import { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { useFocusEffect } from '@react-navigation/native';
import { RootStackParamList } from '../../App';
import { useGhostConnection, SignalingDataJSON } from '../../ghost';
import { useNetworkState } from '../../hooks/useNetworkState';
import {
  NetworkIndicator,
  StatusBanner,
  CandidatesList,
  ConnectionStateDisplay,
  PeerDataInput,
  DisconnectedPanel,
  SignalingDataActions,
} from './components';
import { styles } from './styles';

type ConnectionScreenProps = {
  navigation: NativeStackNavigationProp<RootStackParamList, 'Connection'>;
};

type Step = 'init' | 'gathering' | 'exchange' | 'connecting' | 'connected';

export default function ConnectionScreen({ navigation }: ConnectionScreenProps) {
  const {
    state,
    phase,
    isConnected,
    initialize,
    generateKey,
    startGathering,
    cancelGathering,
    setPeerData,
    connect,
    startTunnel,
    reconnect,
    close,
    getSignalingData,
  } = useGhostConnection();

  const { networkState, isOnline, isWifi, isCellular } = useNetworkState();

  const [peerDataInput, setPeerDataInput] = useState('');
  const [localSignalingData, setLocalSignalingData] = useState<SignalingDataJSON | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [step, setStep] = useState<Step>('init');
  const [wasConnected, setWasConnected] = useState(false);

  const wasInProgressRef = useRef(false);
  const stepRef = useRef(step);
  stepRef.current = step;

  // Derived state
  const status = phase === 'connected' ? 'connected'
    : phase === 'gathering' ? 'gathering'
    : phase === 'connecting' || phase === 'tunnel_starting' || phase === 'reconnecting' ? 'connecting'
    : phase === 'error' ? 'error'
    : 'disconnected';

  const isInitialized = phase !== 'uninitialized';
  const error = state.error;
  const candidates = state.candidates;
  const connectionState = state.connectionState;
  const publicKey = state.publicKey;
  const networkTriggeredReconnect = state.isReconnecting;

  // Initialize client on focus, cleanup on blur
  useFocusEffect(
    React.useCallback(() => {
      const init = async () => {
        if (wasInProgressRef.current) {
          wasInProgressRef.current = false;
          setStep('init');
          setPeerDataInput('');
          setLocalSignalingData(null);
          setWasConnected(false);
        }

        setIsLoading(true);
        await initialize('stun:stun.l.google.com:19302,stun:stun1.l.google.com:19302');
        setIsLoading(false);
      };
      init();

      return () => {
        const currentStep = stepRef.current;
        if (currentStep === 'gathering' || currentStep === 'connecting' || currentStep === 'exchange') {
          console.log('[ConnectionScreen] Leaving during in-progress state, cancelling...');
          wasInProgressRef.current = true;
          cancelGathering();
          close();
        }
      };
    }, [initialize, close, cancelGathering])
  );

  // Update local signaling data when gathering completes
  useEffect(() => {
    if (phase === 'awaiting_peer' || (candidates.length > 0 && phase === 'gathering')) {
      const data = getSignalingData();
      if (data) {
        setLocalSignalingData(data);
      }
    }
  }, [phase, candidates, getSignalingData]);

  // Track connection status changes
  useEffect(() => {
    if (isConnected) {
      setWasConnected(true);
      setStep('connected');
    } else if ((phase === 'disconnected' || phase === 'error') && wasConnected) {
      setStep('exchange');
    }
  }, [isConnected, phase, wasConnected]);

  // Sync step with phase transitions
  useEffect(() => {
    if (phase === 'gathering') {
      setStep('gathering');
    } else if (phase === 'awaiting_peer') {
      setStep('exchange');
    } else if (phase === 'connected') {
      setStep('connected');
    }
  }, [phase]);

  const handleStartGathering = async () => {
    setIsLoading(true);
    const keyResult = await generateKey();
    if (!keyResult) {
      setIsLoading(false);
      return;
    }

    const signalingData = await startGathering();
    setIsLoading(false);
    if (signalingData) {
      setLocalSignalingData(signalingData);
      setStep('gathering');
    }
  };

  const handleSetPeerData = async () => {
    const inputText = peerDataInput
      .replace(/\x00/g, '')
      .replace(/[\x00-\x08\x0B\x0C\x0E-\x1F]/g, '')
      .trim();

    if (!inputText) {
      Alert.alert('Error', 'Please enter peer signaling data');
      return;
    }

    try {
      const peerData: SignalingDataJSON = JSON.parse(inputText);

      if (!peerData.ufrag || !peerData.pwd) {
        Alert.alert('Error', 'Missing required fields: ufrag or pwd');
        return;
      }
      if (!peerData.candidates || peerData.candidates.length === 0) {
        Alert.alert('Error', 'Missing or empty candidates array');
        return;
      }

      setIsLoading(true);
      const success = await setPeerData(inputText);
      setIsLoading(false);

      if (success) {
        setStep('exchange');
        Alert.alert('Success', `Peer data set! ${peerData.candidates.length} candidates loaded.`);
      }
    } catch (err) {
      const errorMsg = err instanceof Error ? err.message : 'Unknown error';
      let details = `Error: ${errorMsg}\n\nInput length: ${inputText.length} chars`;
      if (!inputText.endsWith('}')) {
        details += '\n\nJSON appears truncated (should end with })';
        details += `\n\nLast 30 chars: "${inputText.substring(inputText.length - 30)}"`;
      }
      Alert.alert('Invalid JSON', details);
    }
  };

  const handleConnect = async () => {
    setIsLoading(true);
    setStep('connecting');

    const iceSuccess = await connect(false);
    if (!iceSuccess) {
      setIsLoading(false);
      setStep('exchange');
      return;
    }

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

  const handleReconnect = async () => {
    setIsLoading(true);
    setStep('connecting');

    const success = await reconnect();
    setIsLoading(false);

    if (success) {
      setWasConnected(true);
      setStep('connected');
      Alert.alert('Reconnected!', 'Tunnel re-established successfully.', [
        { text: 'Test Now', onPress: () => navigation.navigate('Test') },
        { text: 'OK' },
      ]);
      return;
    }

    setWasConnected(false);
    setStep('exchange');

    Alert.alert(
      'New Session Required',
      'The connection cannot be restored. You need to start a new session and exchange signaling data with your peer again.',
      [
        { text: 'Start New Session', onPress: handleStartNewSession },
        { text: 'Cancel', style: 'cancel' },
      ]
    );
  };

  const handleStartNewSession = async () => {
    setWasConnected(false);
    setIsLoading(true);

    const keyResult = await generateKey();
    if (!keyResult) {
      setIsLoading(false);
      Alert.alert('Error', 'Failed to generate new keys. Please try again.');
      return;
    }

    const signalingData = await startGathering();
    setIsLoading(false);

    if (signalingData) {
      setLocalSignalingData(signalingData);
      setStep('gathering');
      setPeerDataInput('');
    } else {
      Alert.alert('Error', 'Failed to start gathering. Please try again.');
    }
  };

  return (
    <ScrollView style={styles.container} contentContainerStyle={styles.content}>
      <NetworkIndicator
        isOnline={isOnline}
        isWifi={isWifi}
        isCellular={isCellular}
        networkType={networkState.type}
        isReconnecting={networkTriggeredReconnect}
      />

      <StatusBanner
        status={status}
        isLoading={isLoading}
        isReconnecting={networkTriggeredReconnect}
      />

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
          <CandidatesList candidates={candidates} />
          <SignalingDataActions localSignalingData={localSignalingData} />
        </View>
      )}

      {/* Step 3: Enter Peer Data */}
      {(step === 'gathering' || step === 'exchange') && candidates.length > 0 && (
        <PeerDataInput
          peerDataInput={peerDataInput}
          onPeerDataChange={setPeerDataInput}
          onSetPeerData={handleSetPeerData}
          isLoading={isLoading}
        />
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

      <ConnectionStateDisplay connectionState={connectionState} />

      {/* Connected State */}
      {step === 'connected' && isConnected && (
        <View style={styles.section}>
          <View style={styles.successBox}>
            <Text style={styles.successText}>Tunnel Active</Text>
          </View>
          <TouchableOpacity
            style={styles.button}
            onPress={() => navigation.navigate('Test')}
          >
            <Text style={styles.buttonText}>Test Connectivity</Text>
          </TouchableOpacity>
        </View>
      )}

      {/* Disconnected State */}
      {wasConnected && (phase === 'disconnected' || phase === 'error') && (
        <DisconnectedPanel
          error={error}
          isLoading={isLoading}
          onReconnect={handleReconnect}
          onStartNewSession={handleStartNewSession}
        />
      )}
    </ScrollView>
  );
}
