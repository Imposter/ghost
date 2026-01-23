import { StyleSheet } from 'react-native';

export const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: '#1a1a1a',
  },
  content: {
    padding: 16,
  },
  // Status Banner
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
  // Error Box
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
  // Section
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
  // Stats
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
  // Quick Tests
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
  // Input
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
  // Buttons
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
  // Result
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
  // History
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
  // Warning
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
