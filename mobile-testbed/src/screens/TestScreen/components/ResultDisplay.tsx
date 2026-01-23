import React from 'react';
import { View, Text, ScrollView } from 'react-native';
import { HTTPResultJSON } from '../../../ghost';
import { styles } from '../styles';

interface ResultDisplayProps {
  result: HTTPResultJSON | null;
}

export function ResultDisplay({ result }: ResultDisplayProps) {
  if (!result) return null;

  return (
    <View style={[styles.resultSection, result.success ? styles.resultSuccess : styles.resultError]}>
      <View style={styles.resultHeader}>
        <Text style={styles.resultStatus}>
          {result.success ? 'Success' : 'Failed'}
        </Text>
        <Text style={styles.resultLatency}>{result.latencyMs || 0}ms</Text>
      </View>
      {result.statusCode && (
        <Text style={styles.resultCode}>Status: {result.statusCode}</Text>
      )}
      {result.body && (
        <View style={styles.resultBody}>
          <Text style={styles.resultBodyLabel}>Response:</Text>
          <ScrollView style={styles.resultBodyScroll} horizontal>
            <Text style={styles.resultBodyText}>{result.body}</Text>
          </ScrollView>
        </View>
      )}
      {result.error && (
        <Text style={styles.resultErrorText}>{result.error}</Text>
      )}
    </View>
  );
}
