import React, { useState, useEffect } from 'react';
import {
  View,
  Text,
  TouchableOpacity,
  StyleSheet,
  ScrollView,
  TextInput,
  ActivityIndicator,
} from 'react-native';
import { useGhostConnection, HTTPResultJSON, TunnelStatsJSON } from '../ghost';

export default function TestScreen() {
  const {
    state,
    phase,
    isConnected,
    httpGet,
    httpPost,
    getTunnelStats,
    updateConnectionState,
  } = useGhostConnection();

  const connectionState = state.connectionState;
  const error = state.error;

  const [url, setUrl] = useState('http://10.0.0.1:8080/test');
  const [postBody, setPostBody] = useState('{"message": "Hello from mobile!"}');
  const [result, setResult] = useState<HTTPResultJSON | null>(null);
  const [stats, setStats] = useState<TunnelStatsJSON | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [testHistory, setTestHistory] = useState<Array<{
    type: string;
    url: string;
    success: boolean;
    latency: number;
    timestamp: Date;
  }>>([]);

  // Update stats periodically
  useEffect(() => {
    const interval = setInterval(() => {
      const currentStats = getTunnelStats();
      setStats(currentStats);
      updateConnectionState();
    }, 1000);

    return () => clearInterval(interval);
  }, [getTunnelStats, updateConnectionState]);

  const addToHistory = (type: string, url: string, result: HTTPResultJSON) => {
    setTestHistory(prev => [{
      type,
      url,
      success: result.success,
      latency: result.latencyMs || 0,
      timestamp: new Date(),
    }, ...prev.slice(0, 9)]); // Keep last 10
  };

  const handleGet = async () => {
    setIsLoading(true);
    setResult(null);
    const res = await httpGet(url);
    setResult(res);
    addToHistory('GET', url, res);
    setIsLoading(false);
  };

  const handlePost = async () => {
    setIsLoading(true);
    setResult(null);
    const res = await httpPost(url.replace('/test', '/echo'), 'application/json', postBody);
    setResult(res);
    addToHistory('POST', url.replace('/test', '/echo'), res);
    setIsLoading(false);
  };

  const quickTests = [
    { label: 'Test Endpoint', url: 'http://10.0.0.1:8080/test' },
    { label: 'Health Check', url: 'http://10.0.0.1:8080/health' },
    { label: 'Server Info', url: 'http://10.0.0.1:8080/info' },
  ];

  const renderStats = () => {
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
  };

  const renderResult = () => {
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
  };

  return (
    <ScrollView style={styles.container} contentContainerStyle={styles.content}>
      {/* Connection Status */}
      <View style={[styles.statusBanner, isConnected ? styles.connected : styles.disconnected]}>
        <Text style={styles.statusBannerText}>
          {isConnected ? 'Tunnel Active' : 'Not Connected'}
        </Text>
        {connectionState && (
          <Text style={styles.ipText}>
            {connectionState.localIP} → {connectionState.peerIP}
          </Text>
        )}
      </View>

      {/* Error Display */}
      {error && (
        <View style={styles.errorBox}>
          <Text style={styles.errorText}>{error}</Text>
        </View>
      )}

      {/* Tunnel Stats */}
      {renderStats()}

      {/* Quick Tests */}
      <View style={styles.section}>
        <Text style={styles.sectionTitle}>Quick Tests</Text>
        <View style={styles.quickTestsRow}>
          {quickTests.map((test, i) => (
            <TouchableOpacity
              key={i}
              style={styles.quickTestButton}
              onPress={() => {
                setUrl(test.url);
                handleGet();
              }}
              disabled={isLoading || !isConnected}
            >
              <Text style={styles.quickTestText}>{test.label}</Text>
            </TouchableOpacity>
          ))}
        </View>
      </View>

      {/* Custom URL */}
      <View style={styles.section}>
        <Text style={styles.sectionTitle}>Custom Request</Text>
        <TextInput
          style={styles.input}
          value={url}
          onChangeText={setUrl}
          placeholder="http://10.0.0.1:8080/test"
          placeholderTextColor="#666"
        />
        <View style={styles.buttonRow}>
          <TouchableOpacity
            style={[styles.button, styles.getButton]}
            onPress={handleGet}
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
            onPress={handlePost}
            disabled={isLoading || !isConnected}
          >
            <Text style={styles.buttonText}>POST</Text>
          </TouchableOpacity>
        </View>
      </View>

      {/* POST Body */}
      <View style={styles.section}>
        <Text style={styles.sectionTitle}>POST Body</Text>
        <TextInput
          style={[styles.input, styles.multilineInput]}
          multiline
          numberOfLines={3}
          value={postBody}
          onChangeText={setPostBody}
          placeholder='{"key": "value"}'
          placeholderTextColor="#666"
        />
      </View>

      {/* Result */}
      {renderResult()}

      {/* Test History */}
      {testHistory.length > 0 && (
        <View style={styles.section}>
          <Text style={styles.sectionTitle}>Test History</Text>
          {testHistory.map((item, i) => (
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
      )}

      {/* Warning if not connected */}
      {!isConnected && (
        <View style={styles.warningBox}>
          <Text style={styles.warningText}>
            Connect to a peer first to test the tunnel.
          </Text>
        </View>
      )}
    </ScrollView>
  );
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

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#1a1a1a',
  },
  content: {
    padding: 16,
  },
  statusBanner: {
    padding: 16,
    borderRadius: 12,
    marginBottom: 16,
    alignItems: 'center',
  },
  connected: {
    backgroundColor: '#1B5E20',
  },
  disconnected: {
    backgroundColor: '#424242',
  },
  statusBannerText: {
    color: '#fff',
    fontSize: 18,
    fontWeight: '600',
  },
  ipText: {
    color: '#B2DFDB',
    fontSize: 14,
    marginTop: 4,
    fontFamily: 'monospace',
  },
  errorBox: {
    backgroundColor: '#F44336',
    padding: 12,
    borderRadius: 8,
    marginBottom: 16,
  },
  errorText: {
    color: '#fff',
    fontSize: 14,
  },
  section: {
    backgroundColor: '#2a2a2a',
    borderRadius: 12,
    padding: 16,
    marginBottom: 16,
  },
  sectionTitle: {
    fontSize: 16,
    fontWeight: '600',
    color: '#fff',
    marginBottom: 12,
  },
  statsSection: {
    backgroundColor: '#2a2a2a',
    borderRadius: 12,
    padding: 16,
    marginBottom: 16,
  },
  statsGrid: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    marginBottom: 12,
  },
  statItem: {
    width: '50%',
    paddingVertical: 8,
    alignItems: 'center',
  },
  statValue: {
    color: '#4CAF50',
    fontSize: 20,
    fontWeight: '600',
  },
  statLabel: {
    color: '#888',
    fontSize: 12,
    marginTop: 4,
  },
  statusRow: {
    flexDirection: 'row',
    alignItems: 'center',
    marginTop: 8,
  },
  statusLabel: {
    color: '#888',
    fontSize: 14,
  },
  statusIndicator: {
    width: 10,
    height: 10,
    borderRadius: 5,
    marginHorizontal: 8,
  },
  active: {
    backgroundColor: '#4CAF50',
  },
  inactive: {
    backgroundColor: '#666',
  },
  statusText: {
    color: '#fff',
    fontSize: 14,
  },
  quickTestsRow: {
    flexDirection: 'row',
    flexWrap: 'wrap',
    gap: 8,
  },
  quickTestButton: {
    backgroundColor: '#333',
    paddingHorizontal: 16,
    paddingVertical: 10,
    borderRadius: 8,
    borderWidth: 1,
    borderColor: '#444',
  },
  quickTestText: {
    color: '#fff',
    fontSize: 14,
  },
  input: {
    backgroundColor: '#333',
    borderRadius: 8,
    padding: 12,
    color: '#fff',
    fontSize: 14,
    fontFamily: 'monospace',
    marginBottom: 12,
  },
  multilineInput: {
    minHeight: 80,
    textAlignVertical: 'top',
  },
  buttonRow: {
    flexDirection: 'row',
    gap: 12,
  },
  button: {
    flex: 1,
    padding: 14,
    borderRadius: 8,
    alignItems: 'center',
    justifyContent: 'center',
    minHeight: 48,
  },
  getButton: {
    backgroundColor: '#4CAF50',
  },
  postButton: {
    backgroundColor: '#2196F3',
  },
  buttonText: {
    color: '#fff',
    fontSize: 16,
    fontWeight: '600',
  },
  resultSection: {
    borderRadius: 12,
    padding: 16,
    marginBottom: 16,
  },
  resultSuccess: {
    backgroundColor: '#1B5E20',
  },
  resultError: {
    backgroundColor: '#B71C1C',
  },
  resultHeader: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    marginBottom: 8,
  },
  resultStatus: {
    color: '#fff',
    fontSize: 18,
    fontWeight: '600',
  },
  resultLatency: {
    color: '#B2DFDB',
    fontSize: 14,
  },
  resultCode: {
    color: '#B2DFDB',
    fontSize: 14,
    marginBottom: 8,
  },
  resultBody: {
    marginTop: 8,
  },
  resultBodyLabel: {
    color: '#B2DFDB',
    fontSize: 12,
    marginBottom: 4,
  },
  resultBodyScroll: {
    maxHeight: 100,
  },
  resultBodyText: {
    color: '#fff',
    fontSize: 13,
    fontFamily: 'monospace',
  },
  resultErrorText: {
    color: '#FFCDD2',
    fontSize: 14,
  },
  historyItem: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    alignItems: 'center',
    paddingVertical: 8,
    borderBottomWidth: 1,
    borderBottomColor: '#333',
  },
  historyLeft: {
    flexDirection: 'row',
    alignItems: 'center',
    flex: 1,
  },
  historyMethod: {
    width: 40,
    fontSize: 12,
    fontWeight: '600',
  },
  successText: {
    color: '#4CAF50',
  },
  errorTextSmall: {
    color: '#F44336',
  },
  historyUrl: {
    color: '#888',
    fontSize: 12,
    flex: 1,
    marginLeft: 8,
  },
  historyLatency: {
    color: '#666',
    fontSize: 12,
  },
  warningBox: {
    backgroundColor: '#FF9800',
    padding: 16,
    borderRadius: 12,
    alignItems: 'center',
  },
  warningText: {
    color: '#fff',
    fontSize: 14,
    textAlign: 'center',
  },
});
