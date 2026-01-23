import React from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  ScrollView,
} from 'react-native';
import { NativeStackNavigationProp } from '@react-navigation/native-stack';
import { RootStackParamList } from '../../App';

type HomeScreenProps = {
  navigation: NativeStackNavigationProp<RootStackParamList, 'Home'>;
};

export default function HomeScreen({ navigation }: HomeScreenProps) {
  return (
    <ScrollView style={styles.container} contentContainerStyle={styles.content}>
      <View style={styles.header}>
        <Text style={styles.title}>Ghost Testbed</Text>
        <Text style={styles.subtitle}>
          P2P Encrypted Tunnel Testing
        </Text>
      </View>

      <View style={styles.infoSection}>
        <Text style={styles.infoTitle}>About</Text>
        <Text style={styles.infoText}>
          This app demonstrates ghost-go mobile bindings for secure peer-to-peer
          communication using ICE NAT traversal and WireGuard encryption.
        </Text>
      </View>

      <View style={styles.buttonSection}>
        <TouchableOpacity
          style={styles.primaryButton}
          onPress={() => navigation.navigate('Connection')}
        >
          <Text style={styles.primaryButtonText}>Connect to Peer</Text>
        </TouchableOpacity>

        <TouchableOpacity
          style={styles.secondaryButton}
          onPress={() => navigation.navigate('Test')}
        >
          <Text style={styles.secondaryButtonText}>Test Tunnel</Text>
        </TouchableOpacity>
      </View>

      <View style={styles.infoSection}>
        <Text style={styles.infoTitle}>Quick Start</Text>
        <Text style={styles.infoText}>
          1. Run the desktop server:{'\n'}
          <Text style={styles.code}>go run ./cmd/mobile-demo -role server</Text>
          {'\n\n'}
          2. Tap "Connect to Peer"{'\n'}
          3. Scan or enter the desktop's signaling data{'\n'}
          4. Wait for connection{'\n'}
          5. Use "Test Tunnel" to verify connectivity
        </Text>
      </View>

      <View style={styles.footer}>
        <Text style={styles.footerText}>
          Userspace networking via gvisor/netstack{'\n'}
          No root permissions required
        </Text>
      </View>
    </ScrollView>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#1a1a1a',
  },
  content: {
    padding: 20,
  },
  header: {
    alignItems: 'center',
    marginBottom: 30,
  },
  title: {
    fontSize: 32,
    fontWeight: 'bold',
    color: '#fff',
    marginBottom: 8,
  },
  subtitle: {
    fontSize: 16,
    color: '#888',
  },
  infoSection: {
    backgroundColor: '#2a2a2a',
    borderRadius: 12,
    padding: 16,
    marginBottom: 20,
  },
  infoTitle: {
    fontSize: 18,
    fontWeight: '600',
    color: '#fff',
    marginBottom: 8,
  },
  infoText: {
    fontSize: 14,
    color: '#ccc',
    lineHeight: 22,
  },
  code: {
    fontFamily: 'monospace',
    backgroundColor: '#333',
    color: '#4CAF50',
    padding: 4,
  },
  buttonSection: {
    marginBottom: 20,
  },
  primaryButton: {
    backgroundColor: '#4CAF50',
    borderRadius: 12,
    padding: 16,
    alignItems: 'center',
    marginBottom: 12,
  },
  primaryButtonText: {
    color: '#fff',
    fontSize: 18,
    fontWeight: '600',
  },
  secondaryButton: {
    backgroundColor: '#333',
    borderRadius: 12,
    padding: 16,
    alignItems: 'center',
    borderWidth: 1,
    borderColor: '#4CAF50',
  },
  secondaryButtonText: {
    color: '#4CAF50',
    fontSize: 18,
    fontWeight: '600',
  },
  footer: {
    alignItems: 'center',
    marginTop: 20,
  },
  footerText: {
    fontSize: 12,
    color: '#666',
    textAlign: 'center',
    lineHeight: 20,
  },
});
