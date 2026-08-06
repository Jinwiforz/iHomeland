using System;
using System.Collections.Generic;

namespace IHomeland.Client.PersonalWorldCombat.Infrastructure
{
    /// <summary>
    /// 描述 closed Protobuf field 的 wire type、重复性与嵌套结构。
    /// </summary>
    internal sealed class ClientBattleProtobufFieldRule
    {
        /// <summary>
        /// 创建单一 field rule。
        /// </summary>
        /// <param name="wireType">仅允许 varint=0 或 length-delimited=2。</param>
        /// <param name="repeated">同一 field 是否允许重复出现。</param>
        /// <param name="nested">Length-delimited nested message schema。</param>
        /// <param name="packedVarints">Length-delimited payload 是否为 packed varints。</param>
        internal ClientBattleProtobufFieldRule(
            int wireType,
            bool repeated,
            ClientBattleProtobufSchema nested = null,
            bool packedVarints = false)
        {
            if ((wireType != 0 && wireType != 2) ||
                nested != null && wireType != 2 ||
                packedVarints && wireType != 2 ||
                nested != null && packedVarints)
            {
                throw new ArgumentException(
                    "client battle protobuf field rule is invalid");
            }

            WireType = wireType;
            Repeated = repeated;
            Nested = nested;
            PackedVarints = packedVarints;
        }

        /// <summary>获取 expected wire type。</summary>
        internal int WireType { get; }

        /// <summary>获取 field 是否允许重复。</summary>
        internal bool Repeated { get; }

        /// <summary>获取可选 nested message schema。</summary>
        internal ClientBattleProtobufSchema Nested { get; }

        /// <summary>获取 payload 是否为 packed varints。</summary>
        internal bool PackedVarints { get; }
    }

    /// <summary>
    /// 保存单一 generated message 的 closed field-number schema。
    /// </summary>
    internal sealed class ClientBattleProtobufSchema
    {
        /// <summary>
        /// 创建不允许未知 field 的 schema。
        /// </summary>
        /// <param name="fields">非空 field number 到 rule 的映射。</param>
        internal ClientBattleProtobufSchema(
            IReadOnlyDictionary<int, ClientBattleProtobufFieldRule> fields)
        {
            if (fields == null || fields.Count == 0)
            {
                throw new ArgumentException(
                    "client battle protobuf schema is empty",
                    nameof(fields));
            }

            Fields = fields;
        }

        /// <summary>获取 closed field table。</summary>
        internal IReadOnlyDictionary<int, ClientBattleProtobufFieldRule>
            Fields { get; }
    }

    /// <summary>
    /// 在 generated parser 前验证 canonical varint、known fields、wire types 与 duplicate policy。
    /// </summary>
    internal static class ClientBattleClosedProtobuf
    {
        /// <summary>
        /// 验证完整 payload 精确符合 closed schema。
        /// </summary>
        /// <param name="payload">Nonempty Protobuf bytes。</param>
        /// <param name="schema">消息对应的 frozen schema。</param>
        /// <returns>无 unknown、malformed 或 duplicate scalar 时为 true。</returns>
        internal static bool Validate(
            byte[] payload,
            ClientBattleProtobufSchema schema)
        {
            if (payload == null ||
                payload.Length == 0 ||
                schema == null)
            {
                return false;
            }

            return ValidateSlice(payload, 0, payload.Length, schema);
        }

        /// <summary>
        /// 验证 nested slice 并确保完全消费。
        /// </summary>
        /// <param name="payload">完整 root buffer。</param>
        /// <param name="offset">Slice 起点。</param>
        /// <param name="length">Slice 宽度。</param>
        /// <param name="schema">Nested closed schema。</param>
        /// <returns>全部 field 合法且精确消费时为 true。</returns>
        private static bool ValidateSlice(
            byte[] payload,
            int offset,
            int length,
            ClientBattleProtobufSchema schema)
        {
            var end64 = (long)offset + length;
            if (offset < 0 ||
                length <= 0 ||
                end64 > payload.Length)
            {
                return false;
            }

            var end = (int)end64;
            var cursor = offset;
            var seenScalars = new HashSet<int>();
            while (cursor < end)
            {
                if (!TryReadCanonicalVarint(
                        payload,
                        ref cursor,
                        end,
                        out var tag) ||
                    tag == 0)
                {
                    return false;
                }

                if ((tag >> 3) > int.MaxValue)
                {
                    return false;
                }

                var fieldNumber = (int)(tag >> 3);
                var wireType = checked((int)(tag & 7));
                if (fieldNumber <= 0 ||
                    !schema.Fields.TryGetValue(
                        fieldNumber,
                        out var rule) ||
                    wireType != rule.WireType ||
                    (!rule.Repeated &&
                     !seenScalars.Add(fieldNumber)))
                {
                    return false;
                }

                if (wireType == 0)
                {
                    if (!TryReadCanonicalVarint(
                            payload,
                            ref cursor,
                            end,
                            out _))
                    {
                        return false;
                    }

                    continue;
                }

                if (!TryReadCanonicalVarint(
                        payload,
                        ref cursor,
                        end,
                        out var encodedLength) ||
                    encodedLength == 0 ||
                    encodedLength > int.MaxValue ||
                    cursor >
                    end - checked((int)encodedLength))
                {
                    return false;
                }

                var nestedLength = checked((int)encodedLength);
                if (rule.Nested != null &&
                    !ValidateSlice(
                        payload,
                        cursor,
                        nestedLength,
                        rule.Nested))
                {
                    return false;
                }

                if (rule.PackedVarints &&
                    !ValidatePackedVarints(
                        payload,
                        cursor,
                        nestedLength))
                {
                    return false;
                }

                cursor += nestedLength;
            }

            return cursor == end;
        }

        /// <summary>
        /// 验证 packed uint64 列表由一个或多个 canonical varint 组成。
        /// </summary>
        /// <param name="payload">完整 root buffer。</param>
        /// <param name="offset">Packed slice 起点。</param>
        /// <param name="length">Packed slice 宽度。</param>
        /// <returns>至少包含一个且全部 canonical 时为 true。</returns>
        private static bool ValidatePackedVarints(
            byte[] payload,
            int offset,
            int length)
        {
            var cursor = offset;
            var end = offset + length;
            var count = 0;
            while (cursor < end)
            {
                if (!TryReadCanonicalVarint(
                        payload,
                        ref cursor,
                        end,
                        out _))
                {
                    return false;
                }

                count++;
            }

            return count > 0 && cursor == end;
        }

        /// <summary>
        /// 读取最多十字节的 canonical unsigned varint。
        /// </summary>
        /// <param name="payload">完整 root buffer。</param>
        /// <param name="cursor">调用前后 cursor。</param>
        /// <param name="end">当前 slice exclusive end。</param>
        /// <param name="value">成功时返回 uint64。</param>
        /// <returns>编码未截断、未溢出且最短表示时为 true。</returns>
        private static bool TryReadCanonicalVarint(
            byte[] payload,
            ref int cursor,
            int end,
            out ulong value)
        {
            value = 0;
            var start = cursor;
            for (var index = 0; index < 10; index++)
            {
                if (cursor >= end)
                {
                    return false;
                }

                var current = payload[cursor++];
                if (index == 9 && (current & 0xfe) != 0)
                {
                    return false;
                }

                value |= (ulong)(current & 0x7f) << (index * 7);
                if ((current & 0x80) == 0)
                {
                    return cursor - start == CanonicalVarintBytes(value);
                }
            }

            return false;
        }

        /// <summary>
        /// 计算 uint64 的最短 varint 宽度。
        /// </summary>
        /// <param name="value">待编码 value。</param>
        /// <returns>一到十字节。</returns>
        private static int CanonicalVarintBytes(ulong value)
        {
            var bytes = 1;
            while (value >= 0x80)
            {
                value >>= 7;
                bytes++;
            }

            return bytes;
        }
    }
}
