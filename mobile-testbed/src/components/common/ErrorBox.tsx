import React from 'react';
import { View, Text, StyleSheet } from 'react-native';

interface ErrorBoxProps {
  message: string;
}

export function ErrorBox({ message }: ErrorBoxProps) {
  return (
    <View style={styles.container}>
      <Text style={styles.text}>{message}</Text>
    </View>
  );
}

const styles = StyleSheet.create({
  container: {
    backgroundColor: '#F44336',
    padding: 12,
    borderRadius: 8,
    marginBottom: 16,
  },
  text: {
    color: '#fff',
    fontSize: 14,
  },
});
