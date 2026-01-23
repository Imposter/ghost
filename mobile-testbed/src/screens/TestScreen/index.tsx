import React, { useState, useEffect } from 'react';
import { View, Text, ScrollView } from 'react-native';
import { useGhostConnection, HTTPResultJSON, TunnelStatsJSON } from '../../ghost';
import {
  TunnelStats,
  QuickTests,
  CustomRequest,
  PostBodyInput,
  ResultDisplay,
  TestHistory,
  TestHistoryItem,
  ConnectionStatusBanner,
} from './components';
import { styles } from './styles';

const QUICK_TESTS = [
  { label: 'Test Endpoint', url: 'http://10.0.0.1:8080/test' },
  { label: 'Health Check', url: 'http://10.0.0.1:8080/health' },
  { label: 'Server Info', url: 'http://10.0.0.1:8080/info' },
];

export default function TestScreen() {
  const {
    state,
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
  const [testHistory, setTestHistory] = useState<TestHistoryItem[]>([]);

  // Update stats periodically
  useEffect(() => {
    const interval = setInterval(() => {
      const currentStats = getTunnelStats();
      setStats(currentStats);
      updateConnectionState();
    }, 1000);

    return () => clearInterval(interval);
  }, [getTunnelStats, updateConnectionState]);

  const addToHistory = (type: string, testUrl: string, res: HTTPResultJSON) => {
    setTestHistory(prev => [{
      type,
      url: testUrl,
      success: res.success,
      latency: res.latencyMs || 0,
      timestamp: new Date(),
    }, ...prev.slice(0, 9)]);
  };

  const handleGet = async (testUrl?: string) => {
    const targetUrl = testUrl || url;
    if (testUrl) setUrl(testUrl);

    setIsLoading(true);
    setResult(null);
    const res = await httpGet(targetUrl);
    setResult(res);
    addToHistory('GET', targetUrl, res);
    setIsLoading(false);
  };

  const handlePost = async () => {
    setIsLoading(true);
    setResult(null);
    const postUrl = url.replace('/test', '/echo');
    const res = await httpPost(postUrl, 'application/json', postBody);
    setResult(res);
    addToHistory('POST', postUrl, res);
    setIsLoading(false);
  };

  return (
    <ScrollView style={styles.container} contentContainerStyle={styles.content}>
      <ConnectionStatusBanner
        isConnected={isConnected}
        connectionState={connectionState}
      />

      {error && (
        <View style={styles.errorBox}>
          <Text style={styles.errorText}>{error}</Text>
        </View>
      )}

      <TunnelStats stats={stats} />

      <QuickTests
        tests={QUICK_TESTS}
        onTestPress={handleGet}
        isLoading={isLoading}
        isConnected={isConnected}
      />

      <CustomRequest
        url={url}
        onUrlChange={setUrl}
        onGet={() => handleGet()}
        onPost={handlePost}
        isLoading={isLoading}
        isConnected={isConnected}
      />

      <PostBodyInput
        postBody={postBody}
        onPostBodyChange={setPostBody}
      />

      <ResultDisplay result={result} />

      <TestHistory history={testHistory} />

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
