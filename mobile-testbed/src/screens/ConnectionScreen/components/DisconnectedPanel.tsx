import React from 'react';
import { View, Text, TouchableOpacity } from 'react-native';
import { styles } from '../styles';

interface DisconnectedPanelProps {
  error: string | null;
  isLoading: boolean;
  onReconnect: () => void;
  onStartNewSession: () => void;
}

export function DisconnectedPanel({
  error,
  isLoading,
  onReconnect,
  onStartNewSession,
}: DisconnectedPanelProps) {
  return (
    <View style={styles.section}>
      <View style={styles.disconnectedBox}>
        <Text style={styles.disconnectedText}>Connection Lost</Text>
        <Text style={styles.disconnectedSubtext}>
          {error || 'The tunnel has been disconnected'}
        </Text>
      </View>
      <TouchableOpacity
        style={styles.reconnectButton}
        onPress={onReconnect}
        disabled={isLoading}
      >
        <Text style={styles.buttonText}>
          {isLoading ? 'Reconnecting...' : 'Reconnect'}
        </Text>
      </TouchableOpacity>
      <TouchableOpacity
        style={styles.secondaryButton}
        onPress={onStartNewSession}
        disabled={isLoading}
      >
        <Text style={styles.secondaryButtonText}>Start New Session</Text>
      </TouchableOpacity>
    </View>
  );
}
