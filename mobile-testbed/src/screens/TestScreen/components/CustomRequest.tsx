import React from 'react';
import { View, Text, TextInput, TouchableOpacity, ActivityIndicator } from 'react-native';
import { styles } from '../styles';

interface CustomRequestProps {
  url: string;
  onUrlChange: (url: string) => void;
  onGet: () => void;
  onPost: () => void;
  isLoading: boolean;
  isConnected: boolean;
}

export function CustomRequest({
  url,
  onUrlChange,
  onGet,
  onPost,
  isLoading,
  isConnected,
}: CustomRequestProps) {
  return (
    <View style={styles.section}>
      <Text style={styles.sectionTitle}>Custom Request</Text>
      <TextInput
        style={styles.input}
        value={url}
        onChangeText={onUrlChange}
        placeholder="http://10.0.0.1:8080/test"
        placeholderTextColor="#666"
      />
      <View style={styles.buttonRow}>
        <TouchableOpacity
          style={[styles.button, styles.getButton]}
          onPress={onGet}
          disabled={isLoading || !isConnected}
        >
          {isLoading ? (
            <ActivityIndicator color="#fff" size="small" />
          ) : (
            <Text style={styles.buttonText}>GET</Text>
          )}
        </TouchableOpacity>
        <TouchableOpacity
          style={[styles.button, styles.postButton]}
          onPress={onPost}
          disabled={isLoading || !isConnected}
        >
          <Text style={styles.buttonText}>POST</Text>
        </TouchableOpacity>
      </View>
    </View>
  );
}
