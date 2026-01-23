import React from 'react';
import { View, Text, TouchableOpacity } from 'react-native';
import { styles } from '../styles';

interface QuickTest {
  label: string;
  url: string;
}

interface QuickTestsProps {
  tests: QuickTest[];
  onTestPress: (url: string) => void;
  isLoading: boolean;
  isConnected: boolean;
}

export function QuickTests({ tests, onTestPress, isLoading, isConnected }: QuickTestsProps) {
  return (
    <View style={styles.section}>
      <Text style={styles.sectionTitle}>Quick Tests</Text>
      <View style={styles.quickTestsRow}>
        {tests.map((test, i) => (
          <TouchableOpacity
            key={i}
            style={styles.quickTestButton}
            onPress={() => onTestPress(test.url)}
            disabled={isLoading || !isConnected}
          >
            <Text style={styles.quickTestText}>{test.label}</Text>
          </TouchableOpacity>
        ))}
      </View>
    </View>
  );
}
