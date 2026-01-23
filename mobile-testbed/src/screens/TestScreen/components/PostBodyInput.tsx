import React from 'react';
import { View, Text, TextInput } from 'react-native';
import { styles } from '../styles';

interface PostBodyInputProps {
  postBody: string;
  onPostBodyChange: (body: string) => void;
}

export function PostBodyInput({ postBody, onPostBodyChange }: PostBodyInputProps) {
  return (
    <View style={styles.section}>
      <Text style={styles.sectionTitle}>POST Body</Text>
      <TextInput
        style={[styles.input, styles.multilineInput]}
        multiline
        numberOfLines={3}
        value={postBody}
        onChangeText={onPostBodyChange}
        placeholder='{"key": "value"}'
        placeholderTextColor="#666"
      />
    </View>
  );
}
