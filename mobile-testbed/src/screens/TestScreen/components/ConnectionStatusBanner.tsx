import React from 'react';
import { View, Text } from 'react-native';
import { ConnectionStateJSON } from '../../../ghost';
import { styles } from '../styles';

interface ConnectionStatusBannerProps {
  isConnected: boolean;
  connectionState: ConnectionStateJSON | null;
}

export function ConnectionStatusBanner({ isConnected, connectionState }: ConnectionStatusBannerProps) {
  return (
    <View style={[styles.statusBanner, isConnected ? styles.connected : styles.disconnected]}>
      <Text style={styles.statusBannerText}>
        {isConnected ? 'Tunnel Active' : 'Not Connected'}
      </Text>
      {connectionState && (
        <Text style={styles.ipText}>
          {connectionState.localIP} → {connectionState.peerIP}
        </Text>
      )}
    </View>
  );
}
