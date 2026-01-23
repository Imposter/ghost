import React from 'react';
import { View, Text } from 'react-native';
import { CandidateJSON } from '../../../ghost';
import { styles } from '../styles';

interface CandidatesListProps {
  candidates: CandidateJSON[];
}

export function CandidatesList({ candidates }: CandidatesListProps) {
  if (candidates.length === 0) return null;

  return (
    <View style={styles.candidateSection}>
      <Text style={styles.sectionTitle}>Local Candidates ({candidates.length})</Text>
      {candidates.map((c, i) => (
        <View key={i} style={styles.candidateItem}>
          <Text style={styles.candidateType}>{c.type}</Text>
          <Text style={styles.candidateAddr}>{c.address}:{c.port}</Text>
        </View>
      ))}
    </View>
  );
}
