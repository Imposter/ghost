import React from 'react';
import { View, Text, ActivityIndicator } from 'react-native';
import { styles } from '../styles';

interface NetworkIndicatorProps {
  isOnline: boolean;
  isWifi: boolean;
  isCellular: boolean;
  networkType: string;
  isReconnecting: boolean;
}

export function NetworkIndicator({
  isOnline,
  isWifi,
  isCellular,
  networkType,
  isReconnecting,
}: NetworkIndicatorProps) {
  const getNetworkIcon = () => {
    if (!isOnline) return 'X';
    if (isWifi) return 'W';
    if (isCellular) return 'C';
    return '?';
  };

  const getNetworkLabel = () => {
    if (!isOnline) return 'Offline';
    if (isWifi) return 'WiFi';
    if (isCellular) return 'Cellular';
    return networkType;
  };

  const getNetworkColor = () => {
    if (!isOnline) return '#F44336';
    if (isWifi) return '#4CAF50';
    if (isCellular) return '#2196F3';
    return '#888';
  };

  return (
    <View style={styles.networkIndicator}>
      <View style={[styles.networkIcon, { backgroundColor: getNetworkColor() }]}>
        <Text style={styles.networkIconText}>{getNetworkIcon()}</Text>
      </View>
      <Text style={[styles.networkLabel, { color: getNetworkColor() }]}>
        {getNetworkLabel()}
      </Text>
      {isReconnecting && (
        <View style={styles.reconnectingBadge}>
          <ActivityIndicator size="small" color="#FF9800" />
          <Text style={styles.reconnectingText}>Reconnecting...</Text>
        </View>
      )}
    </View>
  );
}
