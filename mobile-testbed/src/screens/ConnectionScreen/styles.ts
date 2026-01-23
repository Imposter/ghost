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
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    padding: 12,
    borderRadius: 8,
    marginBottom: 16,
  },
  status_disconnected: {
    backgroundColor: '#666',
  },
  status_gathering: {
    backgroundColor: '#2196F3',
  },
  status_connecting: {
    backgroundColor: '#FF9800',
  },
  status_connected: {
    backgroundColor: '#4CAF50',
  },
  status_error: {
    backgroundColor: '#F44336',
  },
  statusText: {
    color: '#fff',
    fontSize: 16,
    fontWeight: '600',
  },
  spinner: {
    marginLeft: 8,
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
  // Sections
  section: {
    backgroundColor: '#2a2a2a',
    borderRadius: 12,
    padding: 16,
    marginBottom: 16,
  },
  sectionTitle: {
    fontSize: 18,
    fontWeight: '600',
    color: '#fff',
    marginBottom: 8,
  },
  helpText: {
    fontSize: 14,
    color: '#888',
    marginBottom: 12,
  },
  // Buttons
  button: {
    backgroundColor: '#4CAF50',
    borderRadius: 8,
    padding: 14,
    alignItems: 'center',
  },
  buttonDisabled: {
    backgroundColor: '#444',
  },
  buttonText: {
    color: '#fff',
    fontSize: 16,
    fontWeight: '600',
  },
  secondaryButton: {
    backgroundColor: '#333',
    borderRadius: 8,
    padding: 12,
    alignItems: 'center',
    borderWidth: 1,
    borderColor: '#4CAF50',
    marginTop: 12,
  },
  secondaryButtonText: {
    color: '#4CAF50',
    fontSize: 14,
    fontWeight: '600',
  },
  buttonRow: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    marginTop: 8,
    gap: 8,
  },
  smallButton: {
    flex: 1,
    backgroundColor: '#333',
    borderRadius: 8,
    padding: 10,
    alignItems: 'center',
    borderWidth: 1,
  },
  smallButtonText: {
    color: '#fff',
    fontSize: 12,
    fontWeight: '500',
  },
  copyButton: {
    borderColor: '#4CAF50',
  },
  consoleButton: {
    borderColor: '#2196F3',
  },
  pasteButton: {
    backgroundColor: '#2196F3',
    borderRadius: 8,
    padding: 12,
    alignItems: 'center',
    marginBottom: 12,
  },
  pasteButtonText: {
    color: '#fff',
    fontSize: 14,
    fontWeight: '600',
  },
  // Key Box
  keyBox: {
    backgroundColor: '#333',
    borderRadius: 8,
    padding: 12,
    marginBottom: 12,
  },
  keyLabel: {
    fontSize: 12,
    color: '#888',
    marginBottom: 4,
  },
  keyValue: {
    fontSize: 14,
    color: '#4CAF50',
    fontFamily: 'monospace',
  },
  // Candidates
  candidateSection: {
    marginTop: 12,
  },
  candidateItem: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    backgroundColor: '#333',
    padding: 8,
    borderRadius: 4,
    marginTop: 4,
  },
  candidateType: {
    color: '#4CAF50',
    fontSize: 12,
    fontWeight: '600',
  },
  candidateAddr: {
    color: '#ccc',
    fontSize: 12,
    fontFamily: 'monospace',
  },
  // Input
  input: {
    backgroundColor: '#333',
    borderRadius: 8,
    padding: 12,
    color: '#fff',
    fontSize: 14,
    fontFamily: 'monospace',
    minHeight: 100,
    textAlignVertical: 'top',
    marginBottom: 12,
  },
  // State Section
  stateSection: {
    backgroundColor: '#2a2a2a',
    borderRadius: 12,
    padding: 16,
    marginBottom: 16,
  },
  stateRow: {
    flexDirection: 'row',
    justifyContent: 'space-between',
    marginTop: 8,
  },
  stateLabel: {
    color: '#888',
    fontSize: 14,
  },
  stateValue: {
    color: '#fff',
    fontSize: 14,
    fontWeight: '500',
  },
  // Success/Disconnected boxes
  successBox: {
    backgroundColor: '#4CAF50',
    borderRadius: 8,
    padding: 16,
    alignItems: 'center',
    marginBottom: 16,
  },
  successText: {
    color: '#fff',
    fontSize: 18,
    fontWeight: '600',
  },
  disconnectedBox: {
    backgroundColor: '#F44336',
    borderRadius: 8,
    padding: 16,
    alignItems: 'center',
    marginBottom: 16,
  },
  disconnectedText: {
    color: '#fff',
    fontSize: 18,
    fontWeight: '600',
    marginBottom: 4,
  },
  disconnectedSubtext: {
    color: '#ffcdd2',
    fontSize: 14,
    textAlign: 'center',
  },
  reconnectButton: {
    backgroundColor: '#FF9800',
    borderRadius: 8,
    padding: 14,
    alignItems: 'center',
    marginBottom: 12,
  },
  // Network indicator
  networkIndicator: {
    flexDirection: 'row',
    alignItems: 'center',
    justifyContent: 'center',
    marginBottom: 12,
    paddingVertical: 8,
  },
  networkIcon: {
    width: 28,
    height: 28,
    borderRadius: 14,
    alignItems: 'center',
    justifyContent: 'center',
    marginRight: 8,
  },
  networkIconText: {
    color: '#fff',
    fontSize: 14,
    fontWeight: '700',
  },
  networkLabel: {
    fontSize: 14,
    fontWeight: '600',
  },
  reconnectingBadge: {
    flexDirection: 'row',
    alignItems: 'center',
    backgroundColor: 'rgba(255, 152, 0, 0.2)',
    borderRadius: 12,
    paddingHorizontal: 10,
    paddingVertical: 4,
    marginLeft: 12,
  },
  reconnectingText: {
    color: '#FF9800',
    fontSize: 12,
    fontWeight: '500',
    marginLeft: 6,
  },
});
