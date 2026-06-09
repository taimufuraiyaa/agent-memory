for i in {1..10}; do
  rm -f ~/.agent-memory/dashboard.agent-memory.pid
  .build/test-binary dashboard --start --no-open > out.log 2> err.log || {
     echo "FAILED ON ATTEMPT $i"
     cat out.log err.log
     exit 1
  }
  .build/test-binary dashboard --stop
done
echo "SUCCESS"
