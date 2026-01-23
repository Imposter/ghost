import React from 'react';
import { View, Text, ActivityIndicator } from 'react-native';
import { styles } from '../styles';

type ConnectionStatus = 'disconnected' | 'gathering' | 'connecting' | 'connected' | 'error';

interface StatusBannerProps {
  status: ConnectionStatus;
  isLoading: boolean;
  isReconnecting: boolean;
}

export function StatusBanner({ status, isLoading, isReconnecting }: StatusBannerProps) {
  const statusStyleKey = `status_${status}` as keyof typeof styles;
  const statusStyle = styles[statusStyleKey] as object;

  return (
    <View style={[styles.statusBanner, statusStyle]}>
      <Text style={styles.statusText}>
        {status.charAt(0).toUpperCase() + status.slice(1)}
      </Text>
      {(isLoading || isReconnecting) && (
        <ActivityIndicator color="#fff" style={styles.spinner} />
      )}
    </View>
  );
}
