import React from 'react';
import { View, Text } from 'react-native';
import { styles } from '../styles';

export interface TestHistoryItem {
  type: string;
  url: string;
  success: boolean;
  latency: number;
  timestamp: Date;
}

interface TestHistoryProps {
  history: TestHistoryItem[];
}

export function TestHistory({ history }: TestHistoryProps) {
  if (history.length === 0) return null;

  return (
    <View style={styles.section}>
      <Text style={styles.sectionTitle}>Test History</Text>
      {history.map((item, i) => (
        <View key={i} style={styles.historyItem}>
          <View style={styles.historyLeft}>
            <Text style={[styles.historyMethod, item.success ? styles.successText : styles.errorTextSmall]}>
              {item.type}
            </Text>
            <Text style={styles.historyUrl} numberOfLines={1}>{item.url}</Text>
          </View>
          <Text style={styles.historyLatency}>{item.latency}ms</Text>
        </View>
      ))}
    </View>
  );
}
