/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { testMultiKey } from '../api'
import type { MultiKeyTestResult } from '../types'

const MULTI_KEY_TEST_CONCURRENCY = 3

type MultiKeyTestRunState = 'idle' | 'running' | 'completed' | 'stopped'

type UseMultiKeyTestOptions = {
  channelId: number
  open: boolean
}

/** Coordinates targeted key tests while keeping results local to the dialog session. */
export function useMultiKeyTest(options: UseMultiKeyTestOptions) {
  const [results, setResults] = useState<Map<number, MultiKeyTestResult>>(
    () => new Map()
  )
  const [testingKeys, setTestingKeys] = useState<Set<number>>(() => new Set())
  const [runState, setRunState] = useState<MultiKeyTestRunState>('idle')
  const [runTotal, setRunTotal] = useState(0)
  const [runCompleted, setRunCompleted] = useState(0)
  const runIdRef = useRef(0)
  const stopRef = useRef(false)

  const runKey = useCallback(
    async (
      keyIndex: number,
      runId: number,
      trackBatchProgress: boolean
    ): Promise<void> => {
      setTestingKeys((current) => new Set(current).add(keyIndex))
      let result: MultiKeyTestResult
      try {
        const response = await testMultiKey(options.channelId, keyIndex)
        result = { ...response, key_index: keyIndex, tested_at: Date.now() }
      } catch (error: unknown) {
        result = {
          key_index: keyIndex,
          success: false,
          classification: 'network_error',
          message: error instanceof Error ? error.message : '',
          tested_at: Date.now(),
        }
      }

      if (runIdRef.current === runId) {
        setResults((current) => new Map(current).set(keyIndex, result))
        setTestingKeys((current) => {
          const next = new Set(current)
          next.delete(keyIndex)
          return next
        })
        if (trackBatchProgress) {
          setRunCompleted((current) => current + 1)
        }
      }
    },
    [options.channelId]
  )

  const testKey = useCallback(
    async (keyIndex: number): Promise<void> => {
      if (testingKeys.has(keyIndex)) return
      await runKey(keyIndex, runIdRef.current, false)
    },
    [runKey, testingKeys]
  )

  const startBatch = useCallback(
    async (keyIndexes: number[], clearExisting = false): Promise<void> => {
      if (runState === 'running' || keyIndexes.length === 0) return
      const runId = runIdRef.current + 1
      runIdRef.current = runId
      stopRef.current = false
      setRunState('running')
      setRunTotal(keyIndexes.length)
      setRunCompleted(0)
      setTestingKeys(new Set())
      if (clearExisting) setResults(new Map())

      let cursor = 0
      const worker = async (): Promise<void> => {
        while (!stopRef.current && runIdRef.current === runId) {
          const keyIndex = keyIndexes[cursor]
          cursor += 1
          if (keyIndex === undefined) return
          await runKey(keyIndex, runId, true)
        }
      }
      const workerCount = Math.min(
        MULTI_KEY_TEST_CONCURRENCY,
        keyIndexes.length
      )
      await Promise.all(Array.from({ length: workerCount }, () => worker()))
      if (runIdRef.current === runId) {
        setRunState(stopRef.current ? 'stopped' : 'completed')
      }
    },
    [runKey, runState]
  )

  const stopBatch = useCallback(() => {
    stopRef.current = true
    setRunState('stopped')
  }, [])

  const reset = useCallback(() => {
    runIdRef.current += 1
    stopRef.current = true
    setResults(new Map())
    setTestingKeys(new Set())
    setRunState('idle')
    setRunTotal(0)
    setRunCompleted(0)
  }, [])

  useEffect(() => {
    if (!options.open) reset()
  }, [options.open, reset])

  const summary = useMemo(() => {
    const values = [...results.values()]
    return {
      completed: values.length,
      available: values.filter((result) => result.success).length,
      abnormal: values.filter((result) => !result.success).length,
    }
  }, [results])

  return {
    results,
    testingKeys,
    runState,
    runTotal,
    runCompleted,
    summary,
    testKey,
    startBatch,
    stopBatch,
    reset,
  }
}
