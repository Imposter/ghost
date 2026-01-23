import React from 'react';
import { View, Text } from 'react-native';
import { TunnelStatsJSON } from '../../../ghost';
import { styles } from '../styles';

interface TunnelStatsProps {
  stats: TunnelStatsJSON | null;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

function formatTime(unixSeconds: number): string {
  const date = new Date(unixSeconds * 1000);
  return date.toLocaleTimeString();
}

export function TunnelStats({ stats }: TunnelStatsProps) {
  if (!stats) return null;

  return (
    <View style={styles.statsSection}>
      <Text style={styles.sectionTitle}>Tunnel Statistics</Text>
      <View style={styles.statsGrid}>
        <View style={styles.statItem}>
          <Text style={styles.statValue}>{formatBytes(stats.bytesSent)}</Text>
          <Text style={styles.statLabel}>Sent</Text>
        </View>
        <View style={styles.statItem}>
          <Text style={styles.statValue}>{formatBytes(stats.bytesReceived)}</Text>
          <Text style={styles.statLabel}>Received</Text>
        </View>
        <View style={styles.statItem}>
          <Text style={styles.statValue}>{stats.packetsSent}</Text>
          <Text style={styles.statLabel}>Packets Out</Text>
        </View>
        <View style={styles.statItem}>
          <Text style={styles.statValue}>{stats.packetsReceived}</Text>
          <Text style={styles.statLabel}>Packets In</Text>
        </View>
      </View>
      <View style={styles.statusRow}>
        <Text style={styles.statusLabel}>Tunnel Active:</Text>
        <View style={[styles.statusIndicator, stats.isActive ? styles.active : styles.inactive]} />
        <Text style={styles.statusText}>{stats.isActive ? 'Yes' : 'No'}</Text>
      </View>
      {stats.lastHandshake > 0 && (
        <View style={styles.statusRow}>
          <Text style={styles.statusLabel}>Last Handshake:</Text>
          <Text style={styles.statusText}>{formatTime(stats.lastHandshake)}</Text>
        </View>
      )}
    </View>
  );
}
