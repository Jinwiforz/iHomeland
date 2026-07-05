using System;
using System.Collections.Generic;
using System.IO;
using UnityEngine;

namespace App.Systems
{
    public enum AppLogLevel
    {
        Debug = 0,
        Info = 1,
        Warning = 2,
        Error = 3
    }

    public readonly struct LogEntry
    {
        public readonly DateTime Time;
        public readonly AppLogLevel Level;
        public readonly string Tag;
        public readonly string Message;
        public readonly string StackTrace;

        public LogEntry(DateTime time, AppLogLevel level, string tag, string message, string stackTrace)
        {
            Time = time;
            Level = level;
            Tag = string.IsNullOrWhiteSpace(tag) ? "App" : tag;
            Message = message ?? string.Empty;
            StackTrace = stackTrace ?? string.Empty;
        }

        public string ToLine()
        {
            string timeText = Time.ToString("yyyy-MM-dd HH:mm:ss.fff");

            if (string.IsNullOrWhiteSpace(StackTrace))
            {
                return $"[{timeText}] [{Level}] [{Tag}] {Message}";
            }

            return $"[{timeText}] [{Level}] [{Tag}] {Message}\n{StackTrace}";
        }
    }

    public sealed class LogSystem : AppSystemBase
    {
        private const int DefaultMaxMemoryEntries = 1000;

        private readonly Queue<LogEntry> _entries = new();
        private readonly HashSet<string> _disabledTags = new();

        private StreamWriter _writer;
        private string _logFilePath;
        private bool _isWritingToUnityConsole;

        public bool EnableUnityConsole { get; set; } = true;
        public bool EnableFileLog { get; set; } = true;
        public bool CaptureUnityLogs { get; set; } = true;

        public AppLogLevel MinLevel { get; private set; } = AppLogLevel.Debug;

        public string LogFilePath => _logFilePath;

        public int Count => _entries.Count;

        protected override void OnInitialize()
        {
            CreateLogFile();

            if (CaptureUnityLogs)
            {
                Application.logMessageReceived += OnUnityLogReceived;
            }

            Info<LogSystem>("Log system initialized.");
        }

        protected override void OnShutdown()
        {
            Info<LogSystem>("Log system shutdown.");

            if (CaptureUnityLogs)
            {
                Application.logMessageReceived -= OnUnityLogReceived;
            }

            _writer?.Flush();
            _writer?.Dispose();
            _writer = null;
        }

        public void Debug<T>(string message)
        {
            Debug(message, typeof(T).Name);
        }

        public void Info<T>(string message)
        {
            Info(message, typeof(T).Name);
        }

        public void Warning<T>(string message)
        {
            Warning(message, typeof(T).Name);
        }

        public void Error<T>(string message)
        {
            Error(message, typeof(T).Name);
        }

        public void Debug(string message, string tag = "App")
        {
            Write(AppLogLevel.Debug, tag, message, null, true);
        }

        public void Info(string message, string tag = "App")
        {
            Write(AppLogLevel.Info, tag, message, null, true);
        }

        public void Warning(string message, string tag = "App")
        {
            Write(AppLogLevel.Warning, tag, message, null, true);
        }

        public void Error(string message, string tag = "App")
        {
            Write(AppLogLevel.Error, tag, message, null, true);
        }

        public void SetMinLevel(AppLogLevel level)
        {
            MinLevel = level;
        }

        public void SetTagEnabled(string tag, bool enabled)
        {
            if (string.IsNullOrWhiteSpace(tag))
            {
                return;
            }

            if (enabled)
            {
                _disabledTags.Remove(tag);
            }
            else
            {
                _disabledTags.Add(tag);
            }
        }

        public bool IsTagEnabled(string tag)
        {
            if (string.IsNullOrWhiteSpace(tag))
            {
                return true;
            }

            return !_disabledTags.Contains(tag);
        }

        public LogEntry[] GetRecentEntries()
        {
            return _entries.ToArray();
        }

        private void Write(AppLogLevel level, string tag, string message, string stackTrace, bool outputToUnityConsole)
        {
            if (!ShouldLog(level, tag))
            {
                return;
            }

            LogEntry entry = new LogEntry(DateTime.Now, level, tag, message, stackTrace);

            AddToMemory(entry);
            WriteToFile(entry);

            if (outputToUnityConsole && EnableUnityConsole)
            {
                WriteToUnityConsole(entry);
            }
        }

        private bool ShouldLog(AppLogLevel level, string tag)
        {
            if (level < MinLevel)
            {
                return false;
            }

            if (!IsTagEnabled(tag))
            {
                return false;
            }

            return true;
        }

        private void AddToMemory(LogEntry entry)
        {
            _entries.Enqueue(entry);

            while (_entries.Count > DefaultMaxMemoryEntries)
            {
                _entries.Dequeue();
            }
        }

        private void CreateLogFile()
        {
            if (!EnableFileLog)
            {
                return;
            }

            try
            {
                string logDirectory = Path.Combine(Application.persistentDataPath, "Logs");

                if (!Directory.Exists(logDirectory))
                {
                    Directory.CreateDirectory(logDirectory);
                }

                string fileName = $"client_{DateTime.Now:yyyyMMdd_HHmmss}.log";

                _logFilePath = Path.Combine(logDirectory, fileName);

                _writer = new StreamWriter(_logFilePath, true);
                _writer.AutoFlush = true;
            }
            catch (Exception exception)
            {
                EnableFileLog = false;
                UnityEngine.Debug.LogError($"[LogSystem] Create log file failed: {exception}");
            }
        }

        private void WriteToFile(LogEntry entry)
        {
            if (!EnableFileLog || _writer == null)
            {
                return;
            }

            try
            {
                _writer.WriteLine(entry.ToLine());
            }
            catch (Exception exception)
            {
                EnableFileLog = false;
                UnityEngine.Debug.LogError($"[LogSystem] Write log file failed: {exception}");
            }
        }

        private void WriteToUnityConsole(LogEntry entry)
        {
            _isWritingToUnityConsole = true;

            string line = entry.ToLine();

            switch (entry.Level)
            {
                case AppLogLevel.Debug:
                case AppLogLevel.Info:
                    UnityEngine.Debug.Log(line);
                    break;

                case AppLogLevel.Warning:
                    UnityEngine.Debug.LogWarning(line);
                    break;

                case AppLogLevel.Error:
                    UnityEngine.Debug.LogError(line);
                    break;

                default:
                    UnityEngine.Debug.Log(line);
                    break;
            }

            _isWritingToUnityConsole = false;
        }

        private void OnUnityLogReceived(string condition, string stackTrace, LogType type)
        {
            if (_isWritingToUnityConsole)
            {
                return;
            }

            AppLogLevel level = ConvertUnityLogType(type);

            Write(level, "Unity", condition, stackTrace, false);
        }

        private static AppLogLevel ConvertUnityLogType(LogType type)
        {
            return type switch
            {
                LogType.Error => AppLogLevel.Error,
                LogType.Assert => AppLogLevel.Error,
                LogType.Exception => AppLogLevel.Error,
                LogType.Warning => AppLogLevel.Warning,
                LogType.Log => AppLogLevel.Info,
                _ => AppLogLevel.Info
            };
        }
    }
}