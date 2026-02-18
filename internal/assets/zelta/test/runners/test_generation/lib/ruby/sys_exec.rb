# lib/sys_exec.rb
require 'open3'
require 'timeout'

module SysExec
  class ExecutionTimeout < StandardError; end
  class SysExecFailed < StandardError; end

  def self.run(cmd, timeout: 30, raise_on_failure: true, debug: true)
    puts "Executing: #{cmd}" if debug

    stdout = ''
    stderr = ''
    status = nil
    pid = nil
    timed_out = false

    # handle timeouts, always raise an exception if the command times out
    begin
      Open3.popen3(cmd) do |stdin, out, err, wait_thr|
        pid = wait_thr.pid
        stdin.close

        timed_out = read_streams_with_timeout(out, err, stdout, stderr, wait_thr, timeout)

        if timed_out
          Process.kill('TERM', pid) rescue nil
          sleep 0.1
          Process.kill('KILL', pid) rescue nil
        else
          status = wait_thr.value
        end
      end
    rescue => e
      timed_out = true
    end

    if timed_out
      raise ExecutionTimeout, error_msg(reason: "Command timed out after #{timeout} seconds",
                                        cmd: cmd, stdout: stdout, stderr: stderr)
    end

    if debug
      puts "STDOUT: #{stdout.encode('UTF-8', invalid: :replace, undef: :replace)}" unless stdout.empty?
      puts "STDERR: #{stderr.encode('UTF-8', invalid: :replace, undef: :replace)}" unless stderr.empty?
      puts "Exit status: #{status&.exitstatus}"
    end

    # if command failed and raise_on_failure is true, raise an exception
    if status && status.exitstatus != 0 && raise_on_failure
      raise SysExecFailed,error_msg(reason: "Command failed with exit status #{status.exitstatus}",
                                    cmd: cmd, stdout: stdout, stderr: stderr)
    end

    { stdout: stdout, stderr: stderr, exit_status: status&.exitstatus }
  end
  def self.read_streams_with_timeout(out, err, stdout, stderr, wait_thr, timeout)
    start_time = Time.now

    loop do
      if Time.now - start_time > timeout
        return true
      end

      # Use select to check if data is available
      ready = IO.select([out, err], nil, nil, 0.1)
      if ready
        ready[0].each do |io|
          begin
            data = io.read_nonblock(1024)
            data = data.force_encoding('UTF-8').encode('UTF-8', invalid: :replace, undef: :replace)
            stdout << data if io == out
            stderr << data if io == err
          rescue IO::WaitReadable
            # Nothing available right now
          rescue EOFError
            # Stream closed
          end
        end
      end

      # Check if process finished
      break unless wait_thr.alive?
    end

    false
  end

  def self.error_msg(reason:, cmd:, stdout:, stderr:)
    env_cmd = cmd.gsub(/\$\{?(\w+)\}?/) { ENV[$1] || "#{$&}:undefined" }

    msg = <<~MSG
        \nERROR: #{reason}
        Command: #{cmd}
        Command with env substitution: #{env_cmd}
        STDOUT so far:
      #{stdout.lines.map { |line| "  : #{line}" }.join}
        STDERR so far:
      #{stderr.lines.map { |line| "  : #{line}" }.join}
    MSG

    msg.lines.map { |line| "*** #{line}" }.join
  end

  private_class_method :error_msg, :read_streams_with_timeout
end
