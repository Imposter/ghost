import React from 'react';
import { View, Text } from 'react-native';
import { ConnectionStateJSON } from '../../../ghost';
import { styles } from '../styles';

interface ConnectionStateDisplayProps {
  connectionState: ConnectionStateJSON | null;
}

export function ConnectionStateDisplay({ connectionState }: ConnectionStateDisplayProps) {
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
}
