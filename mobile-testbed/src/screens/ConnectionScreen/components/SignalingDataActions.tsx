import React from 'react';
import { View, Text, TouchableOpacity, Alert, Clipboard } from 'react-native';
import { SignalingDataJSON } from '../../../ghost';
import { styles } from '../styles';

interface SignalingDataActionsProps {
  localSignalingData: SignalingDataJSON | null;
}

export function SignalingDataActions({ localSignalingData }: SignalingDataActionsProps) {
  if (!localSignalingData) return null;

  const copySignalingData = () => {
    Alert.alert('Signaling Data', JSON.stringify(localSignalingData, null, 2));
  };

  const copyToClipboard = () => {
    const jsonStr = JSON.stringify(localSignalingData);
    Clipboard.setString(jsonStr);
    Alert.alert('Copied!', 'Signaling data copied to clipboard (compact JSON).');
  };

  const printToConsole = () => {
    const jsonStr = JSON.stringify(localSignalingData);
    console.log('\n');
    console.log('='.repeat(60));
    console.log('MOBILE SIGNALING DATA (copy this to desktop):');
    console.log('='.repeat(60));
    console.log(jsonStr);
    console.log('='.repeat(60));
    console.log('\n');
    Alert.alert('Printed!', 'Check your Metro/Expo terminal for the signaling data.');
  };

  return (
    <View>
      <TouchableOpacity style={styles.secondaryButton} onPress={copySignalingData}>
        <Text style={styles.secondaryButtonText}>View Signaling Data</Text>
      </TouchableOpacity>
      <View style={styles.buttonRow}>
        <TouchableOpacity style={[styles.smallButton, styles.copyButton]} onPress={copyToClipboard}>
          <Text style={styles.smallButtonText}>Copy to Clipboard</Text>
        </TouchableOpacity>
        <TouchableOpacity style={[styles.smallButton, styles.consoleButton]} onPress={printToConsole}>
          <Text style={styles.smallButtonText}>Print to Console</Text>
        </TouchableOpacity>
      </View>
    </View>
  );
}
