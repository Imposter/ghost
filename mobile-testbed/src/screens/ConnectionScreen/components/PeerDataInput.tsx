import React from 'react';
import { View, Text, TextInput, TouchableOpacity, Alert, Clipboard } from 'react-native';
import { styles } from '../styles';

interface PeerDataInputProps {
  peerDataInput: string;
  onPeerDataChange: (text: string) => void;
  onSetPeerData: () => void;
  isLoading: boolean;
}

export function PeerDataInput({
  peerDataInput,
  onPeerDataChange,
  onSetPeerData,
  isLoading,
}: PeerDataInputProps) {
  const pasteFromClipboard = async () => {
    try {
      const text = await Clipboard.getString();
      if (text) {
        onPeerDataChange(text);
        Alert.alert('Pasted!', `Pasted ${text.length} characters from clipboard.`);
      } else {
        Alert.alert('Empty', 'Clipboard is empty.');
      }
    } catch (err) {
      Alert.alert('Error', 'Failed to read clipboard.');
    }
  };

  return (
    <View style={styles.section}>
      <Text style={styles.sectionTitle}>Step 3: Enter Peer Data</Text>
      <Text style={styles.helpText}>
        Paste the signaling data from the desktop server.
      </Text>
      <TouchableOpacity style={styles.pasteButton} onPress={pasteFromClipboard}>
        <Text style={styles.pasteButtonText}>Paste from Clipboard</Text>
      </TouchableOpacity>
      <TextInput
        style={styles.input}
        multiline
        numberOfLines={6}
        placeholder='{"ufrag":"...", "pwd":"...", "publicKey":"...", "candidates":[...]}'
        placeholderTextColor="#666"
        value={peerDataInput}
        onChangeText={onPeerDataChange}
        autoCapitalize="none"
        autoCorrect={false}
      />
      <TouchableOpacity
        style={styles.button}
        onPress={onSetPeerData}
        disabled={isLoading}
      >
        <Text style={styles.buttonText}>Set Peer Data</Text>
      </TouchableOpacity>
    </View>
  );
}
